package handler

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestAttachAdmissionObjects(t *testing.T) {
	source := "items = option(\"items\")\n"
	run := testKCLRun("set-annotation", source, map[string]interface{}{
		"annotations": map[string]interface{}{"managed-by": "kcl-operator"},
	})
	kclRunYAML, err := yaml.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	object := mustJSON(nginxPod("nginx:1.15.0"))
	oldObject := mustJSON(nginxPod("nginx:1.14.2"))

	unchanged, err := attachAdmissionObjects(kclRunYAML, object, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(kclRunYAML) {
		t.Fatal("missing oldObject should leave the KCLRun document unchanged")
	}
	unchanged, err = attachAdmissionObjects(kclRunYAML, object, []byte("null"))
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(kclRunYAML) {
		t.Fatal("null oldObject should leave the KCLRun document unchanged")
	}

	if _, err := attachAdmissionObjects(kclRunYAML, object, []byte("{")); err == nil {
		t.Fatal("expected invalid oldObject to fail")
	}
	if _, err := attachAdmissionObjects(kclRunYAML, []byte("[]"), oldObject); err == nil {
		t.Fatal("expected non-object admission payload to fail")
	}

	attached, err := attachAdmissionObjects(kclRunYAML, object, oldObject)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(attached, &doc); err != nil {
		t.Fatal(err)
	}
	spec := doc["spec"].(map[string]interface{})
	if spec["source"] != source {
		t.Fatalf("source changed: %#v", spec["source"])
	}
	params := spec["params"].(map[string]interface{})
	annotations := params["annotations"].(map[string]interface{})
	if annotations["managed-by"] != "kcl-operator" {
		t.Fatalf("params changed: %#v", params)
	}
	admission := spec["admission"].(map[string]interface{})
	gotNew := admission["object"].(map[string]interface{})
	gotOld := admission["oldObject"].(map[string]interface{})
	if gotNew["spec"].(map[string]interface{})["containers"].([]interface{})[0].(map[string]interface{})["image"] != "nginx:1.15.0" {
		t.Fatalf("object: %#v", gotNew)
	}
	if gotOld["spec"].(map[string]interface{})["containers"].([]interface{})[0].(map[string]interface{})["image"] != "nginx:1.14.2" {
		t.Fatalf("oldObject: %#v", gotOld)
	}
	// Integers survive the copy. kyaml keeps the JSON number (80), not a string
	// or a float (80.0), so KCL comparisons against the original object match.
	raw := string(attached)
	if strings.Contains(raw, `"containerPort": "80"`) || strings.Contains(raw, "containerPort: \"80\"") || strings.Contains(raw, "80.0") {
		t.Fatalf("containerPort was coerced:\n%s", raw)
	}
	if !strings.Contains(raw, `"containerPort": 80`) && !strings.Contains(raw, "containerPort: 80") {
		t.Fatalf("containerPort was not preserved as an integer:\n%s", raw)
	}

	oldOnly, err := attachAdmissionObjects(kclRunYAML, nil, oldObject)
	if err != nil {
		t.Fatal(err)
	}
	var oldDoc map[string]interface{}
	if err := yaml.Unmarshal(oldOnly, &oldDoc); err != nil {
		t.Fatal(err)
	}
	oldAdmission := oldDoc["spec"].(map[string]interface{})["admission"].(map[string]interface{})
	if _, ok := oldAdmission["object"]; ok {
		t.Fatal("object should be omitted when the admission request has no object")
	}
	if oldAdmission["oldObject"] == nil {
		t.Fatal("oldObject missing")
	}
}
