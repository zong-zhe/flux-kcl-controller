/*
Copyright 2023 The KCL authors

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

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"kcl-lang.io/kcl-go/pkg/kcl"
	kclapi "kcl-lang.io/kpm/pkg/api"
	kclcli "kcl-lang.io/kpm/pkg/client"
	"kcl-lang.io/kpm/pkg/opt"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kc "github.com/fluxcd/kustomize-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/kcl-lang/kcl-controller/api/v1alpha1"
	krmkcldevfluxcdv1alpha1 "github.com/kcl-lang/kcl-controller/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var testEnv *envtest.Environment
var k8sClient *TestClient
var timeout = time.Second * 100
var interval = time.Millisecond * 250

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.28.3-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// Set the USE_EXISTING_CLUSTER environment variable to true to use an existing cluster.
	err = os.Setenv("USE_EXISTING_CLUSTER", "true")
	Expect(err).NotTo(HaveOccurred())
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = krmkcldevfluxcdv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = sourcev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	client, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(client).NotTo(BeNil())

	k8sClient = &TestClient{
		Client:   client,
		Ctx:      context.Background(),
		Timeout:  timeout,
		Interval: interval,
	}
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

var _ = Describe("create kclrun", func() {

	const (
		testNameSpace = "testkclrun"
		timeout       = time.Second * 100
		interval      = time.Millisecond * 250
	)

	BeforeEach(func() {
		Expect(k8sClient.CleanNamespace(testNameSpace)).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(k8sClient.CleanNamespace(testNameSpace)).NotTo(HaveOccurred())
	})

	It("create kclrun", func() {
		pool := &v1alpha1.KCLRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testcreate",
				Namespace: testNameSpace,
			},
			Spec: v1alpha1.KCLRunSpec{
				SourceRef: kc.CrossNamespaceSourceReference{
					Kind: "GitRepository",
					Name: testNameSpace,
				},
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := k8sClient.Create(ctx, pool)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("gitdeploy", func() {
	const (
		testNameSpace = "test-deploy-cd"
		timeout       = time.Second * 100
		interval      = time.Millisecond * 250
	)

	BeforeEach(func() {
		Expect(k8sClient.CleanNamespace(testNameSpace)).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(k8sClient.RmNamespace(testNameSpace)).NotTo(HaveOccurred())
	})

	Context("When creating KCLRun and GitRepositry", func() {
		It("Should create corresponding Deployment", func() {
			By("By creating a new KCLRun")
			ctx := context.Background()
			kclRun := &v1alpha1.KCLRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNameSpace,
				},
				Spec: v1alpha1.KCLRunSpec{
					SourceRef: kc.CrossNamespaceSourceReference{
						Kind: "GitRepository",
						Name: "test",
					},
				},
			}
			Expect(k8sClient.Create(ctx, kclRun)).Should(Succeed())

			gitRepo := &sourcev1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNameSpace,
				},
				Spec: sourcev1.GitRepositorySpec{
					URL:      "https://github.com/awesome-kusion/kcl-deployment.git",
					Interval: metav1.Duration{Duration: time.Second * 5},
					Reference: &sourcev1.GitRepositoryRef{
						Branch: "main",
					},
				},
			}
			Expect(k8sClient.Create(ctx, gitRepo)).Should(Succeed())

			deploymentLookupKey := types.NamespacedName{Name: "nginx-deployment-6", Namespace: "default"}
			createdDeployment := &appsv1.Deployment{}

			// We'll need to retry getting this newly created Deployment, given that creation may not immediately happen.
			Eventually(func() bool {
				err := k8sClient.Get(ctx, deploymentLookupKey, createdDeployment)
				return err == nil
			}, timeout, interval).Should(BeTrue())
		})
	})
})

var _ = Describe("getd", func() {
	const (
		testNameSpace = "test-deploy-cd"
		timeout       = time.Second * 100
		interval      = time.Millisecond * 250
	)

	BeforeEach(func() {
		Expect(k8sClient.CleanNamespace(testNameSpace)).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(k8sClient.RmNamespace(testNameSpace)).NotTo(HaveOccurred())
	})

	Context("When creating KCLRun and GitRepositry", func() {
		It("Should create corresponding Deployment", func() {
			By("By creating a new KCLRun")
			ctx := context.Background()

			deploymentLookupKey := types.NamespacedName{Name: "nginx-deployment-6", Namespace: "default"}
			createdDeployment := &appsv1.Deployment{}

			// We'll need to retry getting this newly created Deployment, given that creation may not immediately happen.
			Eventually(func() bool {
				err := k8sClient.Get(ctx, deploymentLookupKey, createdDeployment)
				return err == nil
			}, timeout, interval).Should(BeTrue())
		})
	})
})

var _ = Describe("compile", func() {
	const (
		testNameSpace = "test-deploy-cd"
		timeout       = time.Second * 100
		interval      = time.Millisecond * 250
	)

	Context("When creating KCLRun and GitRepositry", func() {
		It("Should create corresponding Deployment", func() {
			By("By creating a new KCLRun")
			// compile the KCL source code into the kubenretes manifests

			opts := opt.DefaultCompileOptions()
			opts.SetHasSettingsYaml(true)

			res, err := kclapi.RunWithOpts(
				opt.WithKclOption(kcl.WithWorkDir("/Users/zongz/Workspace/learn/kcl_learn/kcl-deployment/flask-demo-kcl-manifests")),
				opt.WithKclOption(kcl.WithSettings("/Users/zongz/Workspace/learn/kcl_learn/kcl-deployment/flask-demo-kcl-manifests/kcl.yaml")),
			)

			kpmcli, _ := kclcli.NewKpmClient()
			opts.SetHasSettingsYaml(true)
			opts.SetPkgPath("/Users/zongz/Workspace/learn/kcl_learn/kcl-deployment/flask-demo-kcl-manifests")
			opts.Option.Merge(kcl.WithSettings("/Users/zongz/Workspace/learn/kcl_learn/kcl-deployment/flask-demo-kcl-manifests/kcl.yaml"))
			res, err = kpmcli.CompileWithOpts(opts)

			Expect(err).NotTo(HaveOccurred())
			fmt.Printf("res: %v\n", res)
		})
	})
})
