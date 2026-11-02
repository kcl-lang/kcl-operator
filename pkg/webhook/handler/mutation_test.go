package handler

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	krmkcldevv1alpha1 "kcl-lang.io/kcl-operator/api/kclrun/v1alpha1"
	"kcl-lang.io/krm-kcl/pkg/api"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"github.com/sirupsen/logrus"
	kwhlogrus "github.com/slok/kubewebhook/v2/pkg/log/logrus"
	kwhmodel "github.com/slok/kubewebhook/v2/pkg/model"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMutationHandler_Mutate(t *testing.T) {
	tests := []struct {
		name            string
		kclRunList      *krmkcldevv1alpha1.KCLRunList
		admissionReview *kwhmodel.AdmissionReview
		expectedResult  *kwhmutating.MutatorResult
		expectedError   bool
	}{
		{
			name: "successful mutation",
			kclRunList: &krmkcldevv1alpha1.KCLRunList{
				Items: []krmkcldevv1alpha1.KCLRun{
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "krm.kcl.dev/v1alpha1",
							Kind:       api.KCLRunKind,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "set-annotation",
							Namespace: "default",
						},
						Spec: krmkcldevv1alpha1.KCLRunSpec{
							Source: `
items = [item | {
    metadata.annotations: {"managed-by" = "kcl-operator"}
} for item in option("items")]
`,
						},
					},
				},
			},
			admissionReview: &kwhmodel.AdmissionReview{
				Namespace: "default",
				NewObjectRaw: func() []byte {
					r, _ := yaml.Marshal(map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]interface{}{
							"name": "nginx",
							"annotations": map[string]interface{}{
								"app": "nginx",
							},
						},
						"spec": map[string]interface{}{
							"containers": []map[string]interface{}{
								{
									"name":  "nginx",
									"image": "nginx:1.14.2",
									"ports": []map[string]interface{}{
										{
											"containerPort": 80,
										},
									},
								},
							},
						},
					},
					)
					return r
				}(),
			},
			expectedResult: &kwhmutating.MutatorResult{
				MutatedObject: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]interface{}{
							"name": "nginx",
							"annotations": map[string]interface{}{
								"app":        "nginx",
								"managed-by": "kcl-operator",
							},
						},
						"spec": map[string]interface{}{
							"containers": []map[string]interface{}{
								{
									"name":  "nginx",
									"image": "nginx:1.14.2",
									"ports": []map[string]interface{}{
										{
											"containerPort": 80,
										},
									},
								},
							},
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name: "update mutation reads oldObject and object",
			kclRunList: &krmkcldevv1alpha1.KCLRunList{
				Items: []krmkcldevv1alpha1.KCLRun{
					testKCLRun("set-annotation", `
item = option("items")[0]
admission = option("resource_list").functionConfig.spec.admission
items = [item | {
    metadata.annotations: {
        "managed-by" = option("params").annotations["managed-by"]
        "previous-image" = admission.oldObject.spec.containers[0].image
        "incoming-image" = admission.object.spec.containers[0].image
        "previous-port" = str(admission.oldObject.spec.containers[0].ports[0].containerPort)
    }
}]
`, map[string]interface{}{
						"annotations": map[string]interface{}{
							"managed-by": "kcl-operator",
						},
					}),
				},
			},
			admissionReview: &kwhmodel.AdmissionReview{
				Namespace:    "default",
				Operation:    kwhmodel.OperationUpdate,
				NewObjectRaw: mustJSON(nginxPod("nginx:1.15.0")),
				OldObjectRaw: mustJSON(nginxPod("nginx:1.14.2")),
			},
			expectedResult: mutatedPod("nginx:1.15.0", map[string]interface{}{
				"app":            "nginx",
				"managed-by":     "kcl-operator",
				"previous-image": "nginx:1.14.2",
				"incoming-image": "nginx:1.15.0",
				"previous-port":  "80",
			}),
			expectedError: false,
		},
		{
			name: "update without reading oldObject still mutates the new object",
			kclRunList: &krmkcldevv1alpha1.KCLRunList{
				Items: []krmkcldevv1alpha1.KCLRun{
					testKCLRun("set-annotation", `
items = [item | {
    metadata.annotations: {"managed-by" = "kcl-operator"}
} for item in option("items")]
`, nil),
				},
			},
			admissionReview: &kwhmodel.AdmissionReview{
				Namespace:    "default",
				Operation:    kwhmodel.OperationUpdate,
				NewObjectRaw: mustJSON(nginxPod("nginx:1.15.0")),
				OldObjectRaw: mustJSON(nginxPod("nginx:1.14.2")),
			},
			expectedResult: mutatedPod("nginx:1.15.0", map[string]interface{}{
				"app":        "nginx",
				"managed-by": "kcl-operator",
			}),
			expectedError: false,
		},
		{
			name: "chained KCLRuns both see the original oldObject",
			kclRunList: &krmkcldevv1alpha1.KCLRunList{
				Items: []krmkcldevv1alpha1.KCLRun{
					testKCLRun("set-annotation", `
items = [item | {
    metadata.annotations: {"managed-by" = "kcl-operator"}
} for item in option("items")]
`, nil),
					testKCLRun("record-previous-image", `
item = option("items")[0]
old = option("resource_list").functionConfig.spec.admission.oldObject
items = [item | {
    metadata.annotations: {"previous-image" = old.spec.containers[0].image}
}]
`, nil),
				},
			},
			admissionReview: &kwhmodel.AdmissionReview{
				Namespace:    "default",
				Operation:    kwhmodel.OperationUpdate,
				NewObjectRaw: mustJSON(nginxPod("nginx:1.15.0")),
				OldObjectRaw: mustJSON(nginxPod("nginx:1.14.2")),
			},
			expectedResult: mutatedPod("nginx:1.15.0", map[string]interface{}{
				"app":            "nginx",
				"managed-by":     "kcl-operator",
				"previous-image": "nginx:1.14.2",
			}),
			expectedError: false,
		},
		{
			name: "null oldObject matches a create mutation",
			kclRunList: &krmkcldevv1alpha1.KCLRunList{
				Items: []krmkcldevv1alpha1.KCLRun{
					testKCLRun("set-annotation", `
items = [item | {
    metadata.annotations: {"managed-by" = "kcl-operator"}
} for item in option("items")]
`, nil),
				},
			},
			admissionReview: &kwhmodel.AdmissionReview{
				Namespace:    "default",
				Operation:    kwhmodel.OperationCreate,
				NewObjectRaw: mustJSON(nginxPod("nginx:1.14.2")),
				OldObjectRaw: []byte("null"),
			},
			expectedResult: mutatedPod("nginx:1.14.2", map[string]interface{}{
				"app":        "nginx",
				"managed-by": "kcl-operator",
			}),
			expectedError: false,
		},
		{
			name: "invalid oldObject fails the mutation",
			kclRunList: &krmkcldevv1alpha1.KCLRunList{
				Items: []krmkcldevv1alpha1.KCLRun{
					testKCLRun("set-annotation", `items = option("items")`, nil),
				},
			},
			admissionReview: &kwhmodel.AdmissionReview{
				Namespace:    "default",
				Operation:    kwhmodel.OperationUpdate,
				NewObjectRaw: mustJSON(nginxPod("nginx:1.15.0")),
				OldObjectRaw: []byte("{"),
			},
			expectedResult: &kwhmutating.MutatorResult{},
			expectedError:  true,
		},
		{
			name:            "no KCLRun found",
			kclRunList:      &krmkcldevv1alpha1.KCLRunList{},
			admissionReview: &kwhmodel.AdmissionReview{Namespace: "default"},
			expectedResult:  &kwhmutating.MutatorResult{},
			expectedError:   false,
		},
	}

	logrusLogEntry := logrus.NewEntry(logrus.New())
	logrusLogEntry.Logger.SetLevel(logrus.DebugLevel)
	logger := kwhlogrus.NewLogrus(logrusLogEntry)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			krmkcldevv1alpha1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithLists(test.kclRunList).Build()
			mutationHandler := &MutationHandler{
				Client: client,
				Reader: client,
				Scheme: scheme,
				Logger: logger,
			}
			result, err := mutationHandler.Mutate(context.Background(), test.admissionReview, nil)
			if (err != nil) != test.expectedError {
				t.Errorf("Unexpected error: %v", err)
			}
			if a, b, r := equalMutatorResults(result, test.expectedResult); !r {
				t.Errorf("Unexpected result:\nGot:%v\nExpected:\n%v", string(a), string(b))
			}
			if test.kclRunList != nil {
				for _, item := range test.kclRunList.Items {
					stored := &krmkcldevv1alpha1.KCLRun{}
					if err := client.Get(context.Background(), types.NamespacedName{Namespace: item.Namespace, Name: item.Name}, stored); err != nil {
						t.Fatalf("stored KCLRun %s: %v", item.Name, err)
					}
					if stored.Spec.Source != item.Spec.Source {
						t.Errorf("stored KCLRun %s source was modified", item.Name)
					}
				}
			}
		})
	}
}

func testKCLRun(name, source string, params map[string]interface{}) krmkcldevv1alpha1.KCLRun {
	run := krmkcldevv1alpha1.KCLRun{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "krm.kcl.dev/v1alpha1",
			Kind:       api.KCLRunKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: krmkcldevv1alpha1.KCLRunSpec{
			Source: source,
		},
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			panic(err)
		}
		run.Spec.Params = runtime.RawExtension{Raw: raw}
	}
	return run
}

func nginxPod(image string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]interface{}{
			"name": "nginx",
			"annotations": map[string]interface{}{
				"app": "nginx",
			},
		},
		"spec": map[string]interface{}{
			"containers": []map[string]interface{}{
				{
					"name":  "nginx",
					"image": image,
					"ports": []map[string]interface{}{
						{
							"containerPort": 80,
						},
					},
				},
			},
		},
	}
}

func mutatedPod(image string, annotations map[string]interface{}) *kwhmutating.MutatorResult {
	pod := nginxPod(image)
	pod["metadata"].(map[string]interface{})["annotations"] = annotations
	return &kwhmutating.MutatorResult{
		MutatedObject: &unstructured.Unstructured{Object: pod},
	}
}

func mustJSON(v interface{}) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func equalMutatorResults(a, b *kwhmutating.MutatorResult) (aMutatedObj []byte, bMutatedObj []byte, result bool) {
	if a == nil && b == nil {
		return
	}
	if a == nil || b == nil {
		return
	}
	aMutatedObj, err := yaml.Marshal(a.MutatedObject)
	if err != nil {
		return
	}
	bMutatedObj, err = yaml.Marshal(b.MutatedObject)
	if err != nil {
		return
	}
	if string(aMutatedObj) != string(bMutatedObj) {
		return
	}
	return aMutatedObj, bMutatedObj, true
}
