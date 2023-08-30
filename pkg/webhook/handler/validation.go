package handler

import (
	"bytes"
	"context"
	"net/http"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	krmkcldevv1alpha1 "kcl-lang.io/kcl-operator/api/kclrun/v1alpha1"
	"kcl-lang.io/krm-kcl/pkg/kio"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"
)

// +kubebuilder:webhook:admissionReviewVersions=v1,path=/validate-v1alpha1-kcl-run,mutating=false,failurePolicy=fail,groups="",resources=pods,verbs=create;update,versions=v1,sideEffects=none,name=kcl-run-validating-webhook.kcl-lang.io

// ValidationHandler validates PrometheusRules
type ValidationHandler struct {
	Client  client.Client
	Reader  client.Reader
	Scheme  *runtime.Scheme
	decoder *admission.Decoder
}

// ValidationHandler admits a PrometheusRule if a specific set of Rule labels exist
func (v *ValidationHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	kclRun := &krmkcldevv1alpha1.KCLRun{}
	err := v.Client.Get(ctx, types.NamespacedName{Name: req.AdmissionRequest.Namespace}, kclRun)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	kclRunBytes, err := yaml.Marshal(kclRun)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	in, out := bytes.NewBuffer([]byte{}), bytes.NewBuffer([]byte{})
	in.WriteString("\n---\n")
	in.Write(kclRunBytes)
	pipeline := kio.NewPipeline(in, out, false)
	if err := pipeline.Execute(); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	// the actual mutation is done by a string in JSONPatch style, i.e. we don't _actually_ modify the object, but
	// tell K8S how it should modifiy it
	jsonBytes, err := yaml.YAMLToJSON(out.Bytes())
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, jsonBytes)
}

func (v *ValidationHandler) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}
