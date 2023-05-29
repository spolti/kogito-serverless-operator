// Copyright 2023 Red Hat, Inc. and/or its affiliates
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha08

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/serverlessworkflow/sdk-go/v2/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	//+kubebuilder:scaffold:imports
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var ctx context.Context
var cancel context.CancelFunc

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Webhook Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: false,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "config", "webhook")},
		},
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme := runtime.NewScheme()
	err = AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	err = admissionv1beta1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// start webhook server using Manager
	webhookInstallOptions := &testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             scheme,
		Host:               webhookInstallOptions.LocalServingHost,
		Port:               webhookInstallOptions.LocalServingPort,
		CertDir:            webhookInstallOptions.LocalServingCertDir,
		LeaderElection:     false,
		MetricsBindAddress: "0",
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&KogitoServerlessWorkflow{}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:webhook

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	// wait for the webhook server to get ready
	dialer := &net.Dialer{Timeout: time.Second}
	addrPort := fmt.Sprintf("%s:%d", webhookInstallOptions.LocalServingHost, webhookInstallOptions.LocalServingPort)
	Eventually(func() error {
		conn, err := tls.DialWithDialer(dialer, "tcp", addrPort, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	}).Should(Succeed())

})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

var _ = Describe("Test Serverless Workflow Validating Webhook", func() {
	Context("when templateOwner and templateRepo are the same with owner and repo", func() {

		var swf KogitoServerlessWorkflow
		swf.ObjectMeta.Name = "greeting-fail-no-version"
		swf.ObjectMeta.Namespace = "default"
		annotations := make(map[string]string)
		annotations["sw.kogito.kie.org/description"] = "Greeting example on k8s!"
		swf.ObjectMeta.Annotations = annotations

		swf.Spec.Flow = model.Workflow{
			BaseWorkflow: model.BaseWorkflow{
				ExpressionLang: "jq",
				Start: &model.Start{
					StateName: "ChooseOnLanguage",
				},
			},
		}

		state := &model.States{
			model.State{
				BaseState: model.BaseState{
					Name: "ChooseOnLanguage",
					Type: "sleep",
					End: &model.End{
						Terminate: true,
					},
				},
				SleepState: &model.SleepState{
					Duration: "PT10S",
				},
			},
		}
		swf.Spec.Flow.States = *state

		It("should return validation error on missing version annotation", func() {
			err := k8sClient.Create(context.TODO(), &swf)
			Expect(err).To(HaveOccurred())

			Expect(err.Error()).To(ContainSubstring("admission webhook \"vkogitoserverlessworkflow.kb.io\" denied the request: [Field metadata.annotation.sw.kogito.kie.org.sw.kogito.kie.org/version not set.]"))
		})

		It("should return validation error on the workflow definition", func() {
			swf.ObjectMeta.Annotations["sw.kogito.kie.org/version"] = "0.0.1"
			swf.Spec.Flow.Start.StateName = "wrong"
			err := k8sClient.Create(context.TODO(), &swf)
			Expect(err).To(HaveOccurred())

			Expect(err.Error()).To(ContainSubstring("admission webhook \"vkogitoserverlessworkflow.kb.io\" denied the request: Key: 'Workflow.Start' Error:Field validation for 'Start' failed on the 'startnotexist' tag"))
		})
	})
})
