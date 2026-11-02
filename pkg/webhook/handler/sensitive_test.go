package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/slok/kubewebhook/v2/pkg/log"
	kwhmodel "github.com/slok/kubewebhook/v2/pkg/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	secretPassword = "hunter2-plaintext"
	secretToken    = "dGhpcy1pcy1hLXNlY3JldC10b2tlbg=="
)

func TestFormatMutatedObjectForLog(t *testing.T) {
	pod := "apiVersion: v1\nkind: Pod\nmetadata:\n  name: nginx\nspec:\n  containers:\n  - image: nginx:1.14.2\n    name: nginx\n"
	configMap := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app\ndata:\n  password: not-a-k8s-secret\n"
	secret := strings.Join([]string{
		"apiVersion: v1",
		"kind: Secret",
		"metadata:",
		"  name: db-credentials",
		"  namespace: default",
		"  annotations:",
		"    app: db",
		"    kubectl.kubernetes.io/last-applied-configuration: '{\"stringData\":{\"password\":\"" + secretPassword + "\"}}'",
		"type: Opaque",
		"stringData:",
		"  password: " + secretPassword,
		"data:",
		"  token: " + secretToken,
		"",
	}, "\n")

	tests := []struct {
		name        string
		raw         string
		requestKind string
		wantExact   bool
		wantSubstr  []string
		forbid      []string
	}{
		{
			name:      "pod is unchanged",
			raw:       pod,
			wantExact: true,
		},
		{
			name:      "configmap data is unchanged",
			raw:       configMap,
			wantExact: true,
		},
		{
			name:       "empty stays empty",
			raw:        " \n\t",
			wantExact:  true,
			wantSubstr: nil,
		},
		{
			name: "secret payload is redacted and identity kept",
			raw:  secret,
			wantSubstr: []string{
				"kind: Secret",
				"name: db-credentials",
				"namespace: default",
				"type: Opaque",
				"app: db",
				"password: " + redactedSecretValue,
				"token: " + redactedSecretValue,
				lastAppliedConfigAnnotation + ": " + redactedSecretValue,
			},
			forbid: []string{secretPassword, secretToken},
		},
		{
			name: "multi-doc keeps the pod and redacts the secret",
			raw:  pod + "---\n" + secret,
			wantSubstr: []string{
				"image: nginx:1.14.2",
				"kind: Secret",
				"name: db-credentials",
			},
			forbid: []string{secretPassword, secretToken},
		},
		{
			name:        "request kind secret redacts data even if kind was rewritten",
			requestKind: "v1/Secret",
			raw:         "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: copied\ndata:\n  password: " + secretPassword + "\n",
			wantSubstr:  []string{"kind: ConfigMap", "name: copied", redactedSecretValue},
			forbid:      []string{secretPassword},
		},
		{
			name:        "unparseable secret admission is suppressed",
			requestKind: "Secret",
			raw:         "kind: Secret\nstringData:\n  password: " + secretPassword + "\n: not-yaml\n",
			wantSubstr:  []string{suppressedSecretLog},
			forbid:      []string{secretPassword},
		},
		{
			name:       "unparseable non-secret is left as-is",
			raw:        "kind: Pod\n: not-yaml\nimage: nginx:1.14.2\n",
			wantExact:  true,
			wantSubstr: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := formatMutatedObjectForLog(test.raw, test.requestKind)
			if test.wantExact && got != test.raw {
				t.Fatalf("expected original log text\n got: %q\nwant: %q", got, test.raw)
			}
			for _, want := range test.wantSubstr {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q\nlog:\n%s", want, got)
				}
			}
			for _, forbid := range test.forbid {
				if strings.Contains(got, forbid) {
					t.Errorf("sensitive value %q still present\nlog:\n%s", forbid, got)
				}
			}
		})
	}
}

func TestAdmissionObjectKind(t *testing.T) {
	gvk := &metav1.GroupVersionKind{Version: "v1", Kind: "Secret"}
	if got := admissionObjectKind(context.Background(), &kwhmodel.AdmissionReview{RequestGVK: gvk}, nil); got != "Secret" {
		t.Fatalf("RequestGVK kind = %q", got)
	}

	obj := &unstructured.Unstructured{}
	obj.SetKind("Secret")
	if got := admissionObjectKind(context.Background(), &kwhmodel.AdmissionReview{}, obj); got != "Secret" {
		t.Fatalf("object kind = %q", got)
	}

	ctx := log.CtxWithValues(context.Background(), log.Kv{"kind": "v1/Secret"})
	if got := admissionObjectKind(ctx, &kwhmodel.AdmissionReview{}, nil); got != "v1/Secret" {
		t.Fatalf("context kind = %q", got)
	}
}

func TestSecretSafeLoggerRedactsSecretJSONPatch(t *testing.T) {
	password := secretPassword
	patch := `[{"op":"add","path":"/metadata/annotations/managed-by","value":"kcl-operator"},{"op":"add","path":"/data/token","value":"` + secretToken + `"},{"op":"replace","path":"/stringData/password","value":"` + password + `"},{"op":"replace","path":"/metadata/annotations/kubectl.kubernetes.io~1last-applied-configuration","value":"{\"password\":\"` + password + `\"}"}]`

	t.Run("secret kind", func(t *testing.T) {
		base := &captureLogger{}
		logger := NewSecretSafeLogger(base)
		ctx := log.CtxWithValues(context.Background(), log.Kv{"kind": "v1/Secret"})
		logger.WithCtxValues(ctx).Debugf("Webhook mutating review finished with: '%s' JSON Patch", patch)

		if len(base.msgs) != 1 {
			t.Fatalf("messages = %#v", base.msgs)
		}
		msg := base.msgs[0]
		if strings.Contains(msg, password) || strings.Contains(msg, secretToken) {
			t.Fatalf("secret payload logged: %s", msg)
		}
		if !strings.Contains(msg, redactedSecretValue) || !strings.Contains(msg, "kcl-operator") {
			t.Fatalf("expected redacted patch to keep non-sensitive ops, got %s", msg)
		}
	})

	t.Run("pod kind keeps data values", func(t *testing.T) {
		base := &captureLogger{}
		logger := NewSecretSafeLogger(base)
		ctx := log.CtxWithValues(context.Background(), log.Kv{"kind": "v1/ConfigMap"})
		logger.WithCtxValues(ctx).Debugf("Webhook mutating review finished with: '%s' JSON Patch", patch)
		if len(base.msgs) != 1 || !strings.Contains(base.msgs[0], password) {
			t.Fatalf("non-secret patch should be unchanged, got %#v", base.msgs)
		}
	})

	t.Run("whole object replace", func(t *testing.T) {
		base := &captureLogger{}
		logger := NewSecretSafeLogger(base)
		ctx := log.CtxWithValues(context.Background(), log.Kv{"kind": "Secret"})
		body := `[{"op":"replace","path":"/","value":{"apiVersion":"v1","kind":"Secret","metadata":{"name":"db"},"data":{"token":"` + secretToken + `"},"stringData":{"password":"` + password + `"}}}]`
		logger.WithCtxValues(ctx).Infof("patch %s", body)
		if len(base.msgs) != 1 {
			t.Fatalf("messages = %#v", base.msgs)
		}
		if strings.Contains(base.msgs[0], password) || strings.Contains(base.msgs[0], secretToken) {
			t.Fatalf("secret payload logged: %s", base.msgs[0])
		}
		if !strings.Contains(base.msgs[0], `"name":"db"`) && !strings.Contains(base.msgs[0], `"name": "db"`) {
			t.Fatalf("expected secret identity to remain, got %s", base.msgs[0])
		}
	})
}

func TestNewSecretSafeLoggerNil(t *testing.T) {
	logger := NewSecretSafeLogger(nil)
	logger.Infof("ignored %s", secretPassword)
}

type captureLogger struct {
	msgs []string
}

func (c *captureLogger) Infof(format string, args ...interface{}) {
	c.msgs = append(c.msgs, fmt.Sprintf(format, args...))
}

func (c *captureLogger) Warningf(format string, args ...interface{}) {
	c.msgs = append(c.msgs, fmt.Sprintf(format, args...))
}

func (c *captureLogger) Errorf(format string, args ...interface{}) {
	c.msgs = append(c.msgs, fmt.Sprintf(format, args...))
}

func (c *captureLogger) Debugf(format string, args ...interface{}) {
	c.msgs = append(c.msgs, fmt.Sprintf(format, args...))
}

func (c *captureLogger) WithValues(map[string]interface{}) log.Logger { return c }

func (c *captureLogger) WithCtxValues(context.Context) log.Logger { return c }

func (c *captureLogger) SetValuesOnCtx(ctx context.Context, _ log.Kv) context.Context {
	return ctx
}
