package handler

import (
	"bytes"
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	krmkcldevv1alpha1 "kcl-lang.io/kcl-operator/api/kclrun/v1alpha1"
	"kcl-lang.io/krm-kcl/pkg/kio"

	"github.com/slok/kubewebhook/v2/pkg/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	runtimeyaml "k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	kwhmodel "github.com/slok/kubewebhook/v2/pkg/model"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"
)

//+kubebuilder:rbac:groups=krm.kcl.dev,resources=kclruns,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=krm.kcl.dev,resources=kclruns/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=krm.kcl.dev,resources=kclruns/finalizers,verbs=update

// MutationHandler validates Kubernetes resources using the KCL source.
type MutationHandler struct {
	Client client.Client
	Reader client.Reader
	Scheme *runtime.Scheme
	Logger log.Logger
}

func (v *MutationHandler) Mutate(ctx context.Context, r *kwhmodel.AdmissionReview, obj metav1.Object) (*kwhmutating.MutatorResult, error) {
	// Get the KCL source
	v.Logger.Infof("Get the KCL source list..")
	kclRunList := &krmkcldevv1alpha1.KCLRunList{}
	err := v.Reader.List(ctx, kclRunList, client.InNamespace(r.Namespace))
	if err != nil {
		v.Logger.Errorf("Get KCL source error: %v", err)
		return &kwhmutating.MutatorResult{}, err
	}
	if len(kclRunList.Items) > 0 {
		v.Logger.Infof("Mutating using KCL..")
		// Input Example: https://github.com/kcl-lang/krm-kcl/blob/main/examples/mutation/set-annotations/suite/good.yaml
		in, out := bytes.NewBuffer(r.NewObjectRaw), bytes.NewBuffer([]byte{})
		for _, kclRun := range kclRunList.Items {
			kclRunBytes, err := yaml.Marshal(kclRun)
			if err != nil {
				v.Logger.Errorf("Get KCL source %v bytes error: %v", kclRun.Name, err)
				return &kwhmutating.MutatorResult{}, err
			}
			in.WriteString("\n---\n")
			in.Write(kclRunBytes)
		}
		// Run pipeline to get the result mutated or validated by the KCL source.
		pipeline := kio.NewPipeline(in, out, false)
		if err := pipeline.Execute(); err != nil {
			v.Logger.Errorf("KCL Pipeline exec error: %v", err)
			return &kwhmutating.MutatorResult{}, err
		}
		v.Logger.Infof("Decode Mutate object.. %v", out.String())
		// The actual mutation is done by a string in JSONPatch style, i.e. we don't _actually_ modify the object, but
		// tell K8S how it should modifiy it
		o, _, err := runtimeyaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme).Decode(out.Bytes(), nil, nil)
		if err != nil {
			v.Logger.Errorf("Data decode error %v", err)
			return &kwhmutating.MutatorResult{}, err
		}
		unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
		unstructuredObj := &unstructured.Unstructured{Object: unstructuredMap}
		if err != nil {
			v.Logger.Errorf("Data decode error %v", err)
			return &kwhmutating.MutatorResult{}, err
		}
		v.Logger.Infof("Mutate using KCL finished.")
		return &kwhmutating.MutatorResult{
			MutatedObject: unstructuredObj,
		}, nil
	}
	v.Logger.Infof("No KCL resource found")
	return &kwhmutating.MutatorResult{}, nil
}
