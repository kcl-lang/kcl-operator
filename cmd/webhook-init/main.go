/*
Copyright 2024 The KCL Authors
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
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"k8s.io/client-go/kubernetes"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/sirupsen/logrus"
	kwhlogrus "github.com/slok/kubewebhook/v2/pkg/log/logrus"
)

func main() {
	logrusLogEntry := logrus.NewEntry(logrus.New())
	logrusLogEntry.Logger.SetLevel(logrus.DebugLevel)
	logger := kwhlogrus.NewLogrus(logrusLogEntry)

	var caPEM, serverCertPEM, serverPrivKeyPEM *bytes.Buffer
	// CA config
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2020),
		Subject: pkix.Name{
			Organization: []string{"kcl-lang.io"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	// CA private key
	caPrivKey, err := rsa.GenerateKey(cryptorand.Reader, 4096)
	if err != nil {
		logger.Errorf("unable to generate CA private key: %v", err)
		os.Exit(1)
	}

	// Self signed CA certificate
	caBytes, err := x509.CreateCertificate(cryptorand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		logger.Errorf("unable to self signed CA certificate: %v", err)
		os.Exit(1)
	}

	// PEM encode CA cert
	caPEM = new(bytes.Buffer)
	_ = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	var (
		webhookService, _ = os.LookupEnv("WEBHOOK_SERVICE")
	)
	if webhookService == "" {
		webhookService = "webhook-service"
	}

	commonName := fmt.Sprintf("%s.default.svc", webhookService)
	dnsNames := []string{webhookService,
		fmt.Sprintf("%s.default", webhookService), commonName}

	// server cert config
	cert := &x509.Certificate{
		DNSNames:     dnsNames,
		SerialNumber: big.NewInt(1658),
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"krm.kcl.dev"},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	// server private key
	serverPrivKey, err := rsa.GenerateKey(cryptorand.Reader, 4096)
	if err != nil {
		fmt.Println(err)
	}

	// sign the server cert
	serverCertBytes, err := x509.CreateCertificate(cryptorand.Reader, cert, ca, &serverPrivKey.PublicKey, caPrivKey)
	if err != nil {
		fmt.Println(err)
	}

	// PEM encode the  server cert and key
	serverCertPEM = new(bytes.Buffer)
	_ = pem.Encode(serverCertPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCertBytes,
	})

	serverPrivKeyPEM = new(bytes.Buffer)
	_ = pem.Encode(serverPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverPrivKey),
	})

	var (
		certsPath, _ = os.LookupEnv("CERTS_PATH")
	)
	if certsPath == "" {
		certsPath = "/etc/webhook/certs/"
	}

	err = os.MkdirAll(certsPath, 0666)
	if err != nil {
		logger.Errorf("unable to mkdir %s: %v", certsPath, err)
		os.Exit(1)
	}
	err = WriteFile(filepath.Join(certsPath, "tls.crt"), serverCertPEM)
	if err != nil {
		logger.Errorf("unable to generate tls.crt: %v", err)
		os.Exit(1)
	}

	err = WriteFile(filepath.Join(certsPath, "tls.key"), serverPrivKeyPEM)
	if err != nil {
		logger.Errorf("unable to generate tls.key: %v", err)
		os.Exit(1)
	}
	// Create `MutatingWebhookConfiguration` resources
	err = createMutationConfig(serverCertPEM)
	if err != nil {
		logger.Errorf("unable to create `MutatingWebhookConfiguration` resources: %v", err)
		os.Exit(1)
	}
}

// WriteFile writes data in the file at the given path
func WriteFile(filepath string, sCert *bytes.Buffer) error {
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(sCert.Bytes())
	if err != nil {
		return err
	}
	return nil
}

//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,verbs=get;list;watch;create;update;patch;delete

func createMutationConfig(caCert *bytes.Buffer) error {
	var (
		webhookNamespace, _ = os.LookupEnv("WEBHOOK_NAMESPACE")
		mutationCfgName, _  = os.LookupEnv("MUTATE_CONFIG")
		webhookService, _   = os.LookupEnv("WEBHOOK_SERVICE")
	)
	if webhookService == "" {
		webhookService = "webhook-service"
	}
	config := ctrl.GetConfigOrDie()
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	path := "/mutate"
	fail := admissionregistrationv1.Fail
	sideEffectClassNone := admissionregistrationv1.SideEffectClassNone

	mutateconfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: mutationCfgName,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{{
			Name: "kcl-webhook-server.krm.kcl.dev",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				CABundle: caCert.Bytes(), // CA bundle created earlier
				Service: &admissionregistrationv1.ServiceReference{
					Name:      webhookService,
					Namespace: webhookNamespace,
					Path:      &path,
				},
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{Operations: []admissionregistrationv1.OperationType{
				admissionregistrationv1.Create},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"deployments", "pods"},
				},
			}},
			SideEffects:             &sideEffectClassNone,
			AdmissionReviewVersions: []string{"v1", "v1beta1"},
			FailurePolicy:           &fail,
		}},
	}

	if _, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(context.Background(), mutateconfig, metav1.CreateOptions{}); err != nil {
		return err
	}
	return nil
}
