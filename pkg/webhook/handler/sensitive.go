package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/slok/kubewebhook/v2/pkg/log"
	kwhmodel "github.com/slok/kubewebhook/v2/pkg/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

const (
	// redactedSecretValue replaces Secret payload values in logs.
	redactedSecretValue = "<redacted>"
	// suppressedSecretLog is logged when a Secret payload cannot be redacted safely.
	suppressedSecretLog = "(output suppressed for Secret)"

	lastAppliedConfigAnnotation = "kubectl.kubernetes.io/last-applied-configuration"
)

// formatMutatedObjectForLog returns text safe to write for a decoded mutation result.
// Non-Secret documents are returned unchanged. Secret data, stringData, and
// last-applied-configuration (which embeds the manifest) are redacted.
// requestKind may be a Kind ("Secret") or a webhook log kind ("v1/Secret").
func formatMutatedObjectForLog(raw, requestKind string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	docs, err := splitYAMLDocuments(raw)
	if err != nil {
		if isSecretKind(requestKind) || documentLooksLikeSecret([]byte(raw)) {
			return suppressedSecretLog
		}
		return raw
	}

	forceSecret := isSecretKind(requestKind)
	parts := make([]string, 0, len(docs))
	sensitive := false
	for _, doc := range docs {
		part, secret := renderDocumentForLog(doc, forceSecret)
		if secret {
			sensitive = true
		}
		parts = append(parts, part)
	}
	if !sensitive {
		return raw
	}
	return joinDocuments(parts)
}

// admissionObjectKind reports the kind of the object under admission.
// The pipeline output is also inspected; this is a fallback for when that
// output cannot be parsed or no longer carries kind.
func admissionObjectKind(ctx context.Context, r *kwhmodel.AdmissionReview, obj metav1.Object) string {
	if r != nil && r.RequestGVK != nil && r.RequestGVK.Kind != "" {
		return r.RequestGVK.Kind
	}
	if obj != nil {
		if kinder, ok := obj.(interface{ GetKind() string }); ok {
			if kind := kinder.GetKind(); kind != "" {
				return kind
			}
		}
		if ro, ok := obj.(runtime.Object); ok && ro.GetObjectKind() != nil {
			if kind := ro.GetObjectKind().GroupVersionKind().Kind; kind != "" {
				return kind
			}
		}
	}
	if ctx != nil {
		if kind, ok := log.ValuesFromCtx(ctx)["kind"].(string); ok && kind != "" {
			return kind
		}
	}
	return ""
}

func isSecretKind(kind string) bool {
	kind = strings.Trim(strings.TrimSpace(kind), "/")
	return kind == "Secret" || strings.HasSuffix(kind, "/Secret")
}

func splitYAMLDocuments(raw string) ([][]byte, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(raw)))
	var docs [][]byte
	for {
		doc, err := reader.Read()
		if err == io.EOF {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		docs = append(docs, doc)
	}
}

func joinDocuments(parts []string) string {
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteString("---\n")
		}
		b.WriteString(part)
		if !strings.HasSuffix(part, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderDocumentForLog returns the document to log and whether it contained
// Secret material that had to be removed.
func renderDocumentForLog(doc []byte, forceSecret bool) (string, bool) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(doc, &obj); err != nil || obj == nil {
		if forceSecret || documentLooksLikeSecret(doc) {
			return suppressedSecretLog, true
		}
		return string(doc), false
	}

	kind, _ := obj["kind"].(string)
	kind = strings.TrimSpace(kind)
	isSecret := kind == "Secret"
	if !isSecret && !forceSecret {
		return string(doc), false
	}

	// Call each redaction unconditionally; || would skip later fields.
	changed := redactSecretField(obj, "data")
	changed = redactSecretField(obj, "stringData") || changed
	if isSecret {
		changed = redactLastAppliedAnnotation(obj) || changed
	}
	if !changed {
		// No payload fields. Keep the original text so we do not reformat
		// unrelated documents that merely rode along with a Secret admission.
		return string(doc), false
	}

	out, err := yaml.Marshal(obj)
	if err != nil {
		return suppressedSecretLog, true
	}
	return string(out), true
}

func redactSecretField(obj map[string]interface{}, field string) bool {
	v, ok := obj[field]
	if !ok || v == nil {
		return false
	}
	obj[field] = redactValues(v)
	return true
}

func redactValues(v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return redactedSecretValue
	}
	out := make(map[string]interface{}, len(m))
	for k := range m {
		out[k] = redactedSecretValue
	}
	return out
}

func redactLastAppliedAnnotation(obj map[string]interface{}) bool {
	meta, ok := obj["metadata"].(map[string]interface{})
	if !ok {
		return false
	}
	anns, ok := meta["annotations"].(map[string]interface{})
	if !ok {
		return false
	}
	if _, ok := anns[lastAppliedConfigAnnotation]; !ok {
		return false
	}
	anns[lastAppliedConfigAnnotation] = redactedSecretValue
	return true
}

func redactNestedSecretFields(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok {
		return false
	}
	changed := redactSecretField(m, "data")
	changed = redactSecretField(m, "stringData") || changed
	return redactLastAppliedAnnotation(m) || changed
}

// documentLooksLikeSecret is a fallback for payloads that fail structured parsing.
func documentLooksLikeSecret(doc []byte) bool {
	s := string(doc)
	return strings.Contains(s, "kind: Secret") ||
		strings.Contains(s, "kind:Secret") ||
		strings.Contains(s, `"kind":"Secret"`) ||
		strings.Contains(s, `"kind": "Secret"`)
}

func sensitivePointer(path string) bool {
	switch {
	case path == "/data" || strings.HasPrefix(path, "/data/"):
		return true
	case path == "/stringData" || strings.HasPrefix(path, "/stringData/"):
		return true
	case strings.Contains(path, "last-applied-configuration"):
		return true
	default:
		return false
	}
}

// redactSecretJSONPatch redacts Secret payload values inside a JSON patch.
// The boolean reports whether the returned string should replace the original.
func redactSecretJSONPatch(patch string) (string, bool) {
	trimmed := strings.TrimSpace(patch)
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	var ops []map[string]interface{}
	if err := dec.Decode(&ops); err != nil || len(ops) == 0 {
		return "", false
	}
	for _, op := range ops {
		if _, ok := op["op"].(string); !ok {
			return "", false
		}
		if _, ok := op["path"].(string); !ok {
			return "", false
		}
	}

	changed := false
	for _, op := range ops {
		path := op["path"].(string)
		if path == "" || path == "/" {
			if v, ok := op["value"]; ok && redactNestedSecretFields(v) {
				changed = true
			}
			continue
		}
		if !sensitivePointer(path) {
			continue
		}
		if _, ok := op["value"]; ok {
			op["value"] = redactedSecretValue
			changed = true
		}
	}
	if !changed {
		return "", false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ops); err != nil {
		return suppressedSecretLog, true
	}
	return strings.TrimSpace(buf.String()), true
}

// secretSafeLogger redacts Secret JSON patches logged by kubewebhook.
// The library writes the full admission JSON patch at debug level, and the
// server runs at debug, so a Secret mutation would otherwise leak data and
// stringData through that line.
type secretSafeLogger struct {
	base   log.Logger
	secret bool
}

// NewSecretSafeLogger wraps base so Secret JSON patch values are not logged.
func NewSecretSafeLogger(base log.Logger) log.Logger {
	if base == nil {
		return log.Noop
	}
	return secretSafeLogger{base: base}
}

func (l secretSafeLogger) Infof(format string, args ...interface{}) {
	l.base.Infof(format, l.sanitizeArgs(args)...)
}

func (l secretSafeLogger) Warningf(format string, args ...interface{}) {
	l.base.Warningf(format, l.sanitizeArgs(args)...)
}

func (l secretSafeLogger) Errorf(format string, args ...interface{}) {
	l.base.Errorf(format, l.sanitizeArgs(args)...)
}

func (l secretSafeLogger) Debugf(format string, args ...interface{}) {
	l.base.Debugf(format, l.sanitizeArgs(args)...)
}

func (l secretSafeLogger) WithValues(values map[string]interface{}) log.Logger {
	secret := l.secret
	if kind, ok := values["kind"].(string); ok {
		secret = isSecretKind(kind)
	}
	return secretSafeLogger{base: l.base.WithValues(values), secret: secret}
}

func (l secretSafeLogger) WithCtxValues(ctx context.Context) log.Logger {
	secret := l.secret
	if ctx != nil {
		if kind, ok := log.ValuesFromCtx(ctx)["kind"].(string); ok {
			secret = isSecretKind(kind)
		}
	}
	return secretSafeLogger{base: l.base.WithCtxValues(ctx), secret: secret}
}

func (l secretSafeLogger) SetValuesOnCtx(parent context.Context, values map[string]interface{}) context.Context {
	return l.base.SetValuesOnCtx(parent, values)
}

func (l secretSafeLogger) sanitizeArgs(args []interface{}) []interface{} {
	if !l.secret || len(args) == 0 {
		return args
	}
	out := make([]interface{}, len(args))
	copy(out, args)
	for i, arg := range args {
		s, ok := arg.(string)
		if !ok {
			continue
		}
		if redacted, ok := redactSecretJSONPatch(s); ok {
			out[i] = redacted
		}
	}
	return out
}
