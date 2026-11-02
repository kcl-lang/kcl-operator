package handler

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	krmkcldevv1alpha1 "kcl-lang.io/kcl-operator/api/kclrun/v1alpha1"
	"kcl-lang.io/krm-kcl/pkg/api"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

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
		wantLogSubstr   []string
		forbidLogSubstr []string
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
			wantLogSubstr: []string{
				"Decode Mutate object..",
				"nginx:1.14.2",
				"managed-by",
			},
		},
		{
			name: "secret mutation redacts payload in logs",
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
				RequestGVK: &metav1.GroupVersionKind{
					Version: "v1",
					Kind:    "Secret",
				},
				NewObjectRaw: func() []byte {
					r, _ := yaml.Marshal(map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Secret",
						"metadata": map[string]interface{}{
							"name":      "db-credentials",
							"namespace": "default",
							"annotations": map[string]interface{}{
								"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"v1","kind":"Secret","stringData":{"password":"hunter2-plaintext"}}`,
							},
						},
						"type": "Opaque",
						"stringData": map[string]interface{}{
							"password": "hunter2-plaintext",
						},
						"data": map[string]interface{}{
							"token": "dGhpcy1pcy1hLXNlY3JldC10b2tlbg==",
						},
					})
					return r
				}(),
			},
			expectedResult: &kwhmutating.MutatorResult{
				MutatedObject: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Secret",
						"metadata": map[string]interface{}{
							"name":      "db-credentials",
							"namespace": "default",
							"annotations": map[string]interface{}{
								"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"v1","kind":"Secret","stringData":{"password":"hunter2-plaintext"}}`,
								"managed-by": "kcl-operator",
							},
						},
						"type": "Opaque",
						"stringData": map[string]interface{}{
							"password": "hunter2-plaintext",
						},
						"data": map[string]interface{}{
							"token": "dGhpcy1pcy1hLXNlY3JldC10b2tlbg==",
						},
					},
				},
			},
			expectedError: false,
			wantLogSubstr: []string{
				"Decode Mutate object..",
				"db-credentials",
				"managed-by",
				redactedSecretValue,
			},
			forbidLogSubstr: []string{
				"hunter2-plaintext",
				"dGhpcy1pcy1hLXNlY3JldC10b2tlbg==",
			},
		},
		{
			name:            "no KCLRun found",
			kclRunList:      &krmkcldevv1alpha1.KCLRunList{},
			admissionReview: &kwhmodel.AdmissionReview{Namespace: "default"},
			expectedResult:  &kwhmutating.MutatorResult{},
			expectedError:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			krmkcldevv1alpha1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithLists(test.kclRunList).Build()
			logs := &captureLogger{}
			mutationHandler := &MutationHandler{
				Client: client,
				Reader: client,
				Scheme: scheme,
				Logger: logs,
			}
			result, err := mutationHandler.Mutate(context.Background(), test.admissionReview, nil)
			if (err != nil) != test.expectedError {
				t.Errorf("Unexpected error: %v", err)
			}
			if a, b, r := equalMutatorResults(result, test.expectedResult); !r {
				t.Errorf("Unexpected result:\nGot:%v\nExpected:\n%v", string(a), string(b))
			}
			joined := strings.Join(logs.msgs, "\n")
			for _, want := range test.wantLogSubstr {
				if !strings.Contains(joined, want) {
					t.Errorf("log missing %q\nlogs:\n%s", want, joined)
				}
			}
			for _, forbid := range test.forbidLogSubstr {
				if strings.Contains(joined, forbid) {
					t.Errorf("log contains sensitive value %q", forbid)
				}
			}
		})
	}
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
