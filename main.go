/*
Copyright 2023.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"k8s.io/apimachinery/pkg/runtime"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	clientconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"kcl-lang.io/kcl-go/pkg/env"
	krmkcldevv1alpha1 "kcl-lang.io/kcl-operator/api/kclrun/v1alpha1"
	"kcl-lang.io/kcl-operator/pkg/webhook/handler"

	"github.com/sirupsen/logrus"
	kwhhttp "github.com/slok/kubewebhook/v2/pkg/http"
	kwhlogrus "github.com/slok/kubewebhook/v2/pkg/log/logrus"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"
	//+kubebuilder:scaffold:imports
)

type config struct {
	certFile string
	keyFile  string
	addr     string
}

func initFlags() *config {
	cfg := &config{}

	fl := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fl.StringVar(&cfg.certFile, "tls-cert-file", "", "TLS certificate file")
	fl.StringVar(&cfg.keyFile, "tls-key-file", "", "TLS key file")
	fl.StringVar(&cfg.addr, "addr", ":8081", "The webhook server port")

	_ = fl.Parse(os.Args[1:])
	return cfg
}

var (
	scheme = runtime.NewScheme()
)

func init() {
	// Enable the KCL fast eval mode
	env.EnableFastEvalMode()
	// Register Kubernetes schemes
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(krmkcldevv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	logrusLogEntry := logrus.NewEntry(logrus.New())
	logrusLogEntry.Logger.SetLevel(logrus.DebugLevel)
	logger := kwhlogrus.NewLogrus(logrusLogEntry)

	cfg := initFlags()

	// setup a manager
	logger.Infof("Setup a manager")
	mgr, err := manager.New(clientconfig.GetConfigOrDie(), manager.Options{
		Scheme: scheme,
	})
	if err != nil {
		logger.Errorf("unable to setup controller manager %v", err)
		os.Exit(1)
	}

	// Create our mutator
	mt := &handler.MutationHandler{
		Client: mgr.GetClient(),
		Reader: mgr.GetAPIReader(),
		Scheme: mgr.GetScheme(),
		Logger: logger,
	}

	//+kubebuilder:scaffold:builder

	mcfg := kwhmutating.WebhookConfig{
		ID:      "podAnnotate",
		Mutator: mt,
		Logger:  logger,
	}
	wh, err := kwhmutating.NewWebhook(mcfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating webhook: %s", err)
		os.Exit(1)
	}

	// Get the handler for our webhook.
	whHandler, err := kwhhttp.HandlerFor(kwhhttp.HandlerConfig{Webhook: wh, Logger: logger})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating webhook handler: %s", err)
		os.Exit(1)
	}
	logger.Infof("Webhook server Listening on %s", cfg.addr)
	err = http.ListenAndServeTLS(cfg.addr, cfg.certFile, cfg.keyFile, whHandler)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error serving webhook: %s", err)
		os.Exit(1)
	}
}
