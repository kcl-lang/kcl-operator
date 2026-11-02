package handler

import (
	"bytes"
	"fmt"

	kyaml "sigs.k8s.io/kustomize/kyaml/yaml"
)

// admissionSpecKey is added to the KCLRun document copied into the KCL
// pipeline. It is not part of the KCLRun CRD and is never written back to
// the API server.
const admissionSpecKey = "admission"

// attachAdmissionObjects copies the admission object and oldObject onto
// spec.admission of a KCLRun manifest before it is passed to krm-kcl.
//
// krm-kcl treats every non-KCLRun document as an item in option("items") and
// drops documents that fail KCLRun matchConstraints. Placing oldObject in
// that list would change existing mutation scripts and would hide it from
// scripts that only match the target kind. The function config is passed
// through as option("resource_list").functionConfig, so both objects stay
// readable there while option("items") remains the resource to mutate.
//
// CREATE requests have no oldObject. The KCLRun bytes are returned unchanged
// in that case, including when objectRaw is set.
func attachAdmissionObjects(kclRunYAML, objectRaw, oldObjectRaw []byte) ([]byte, error) {
	oldObject, hasOld, err := parseAdmissionObject(oldObjectRaw)
	if err != nil {
		return nil, err
	}
	if !hasOld {
		return kclRunYAML, nil
	}
	object, hasObject, err := parseAdmissionObject(objectRaw)
	if err != nil {
		return nil, err
	}

	run, err := kyaml.Parse(string(kclRunYAML))
	if err != nil {
		return nil, fmt.Errorf("parse KCLRun: %w", err)
	}
	admission, err := kyaml.FromMap(map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	if hasObject {
		if err := admission.PipeE(kyaml.SetField("object", object)); err != nil {
			return nil, fmt.Errorf("set admission object: %w", err)
		}
	}
	if err := admission.PipeE(kyaml.SetField("oldObject", oldObject)); err != nil {
		return nil, fmt.Errorf("set admission oldObject: %w", err)
	}
	if err := run.PipeE(
		kyaml.LookupCreate(kyaml.MappingNode, "spec"),
		kyaml.SetField(admissionSpecKey, admission),
	); err != nil {
		return nil, fmt.Errorf("set spec.admission: %w", err)
	}
	out, err := run.String()
	if err != nil {
		return nil, fmt.Errorf("encode KCLRun: %w", err)
	}
	return []byte(out), nil
}

// parseAdmissionObject decodes one admission payload (JSON from the API
// server, or YAML in tests). Empty and JSON null payloads are absent.
func parseAdmissionObject(raw []byte) (*kyaml.RNode, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false, nil
	}
	node, err := kyaml.Parse(string(raw))
	if err != nil {
		return nil, false, fmt.Errorf("decode admission object: %w", err)
	}
	if node == nil || node.YNode() == nil || node.IsNil() {
		return nil, false, nil
	}
	if node.YNode().Kind == kyaml.ScalarNode && (node.YNode().Tag == "!!null" || node.YNode().Value == "null" || node.YNode().Value == "") {
		return nil, false, nil
	}
	if node.YNode().Kind != kyaml.MappingNode {
		return nil, false, fmt.Errorf("decode admission object: expected a mapping")
	}
	return node, true, nil
}
