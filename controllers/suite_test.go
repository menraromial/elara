/*
Copyright 2024 The Elara Authors.

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

package controllers

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var ctx context.Context
var cancel context.CancelFunc

// TestAPIs is the main test entry point.
func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller E2E Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// Add our custom scheme (ElaraPolicy) and the core Kubernetes schemes to the client.
	err = greenopsv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = appsv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = corev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Start the controller manager in a goroutine so the test can run.
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&ElaraPolicyReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()

})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})


// cleanUpTestResources performs a robust, manual cleanup of all resources created
// during a test. It explicitly deletes all namespaced resources before deleting
// the namespace itself to prevent hangs in the envtest environment.
func cleanUpTestResources(ctx context.Context, namespaceName string, policyName string, nodeNames ...string) {
	By("Cleaning up test resources")

	// Step 1: Delete the ElaraPolicy first to stop the controller from reconciling.
	policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: policyName}}
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())

	// Step 2: Delete any cluster-scoped resources like Nodes.
	for _, nodeName := range nodeNames {
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())
	}

	// Step 3: Explicitly delete all deployments within the test namespace.
	By(fmt.Sprintf("Explicitly deleting all deployments in namespace '%s'", namespaceName))
	deploymentList := &appsv1.DeploymentList{}
	Expect(k8sClient.List(ctx, deploymentList, client.InNamespace(namespaceName))).Should(Succeed())
	for _, dep := range deploymentList.Items {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &dep))).Should(Succeed())
	}

	// Step 4: Wait for all deployments to be fully gone. This is CRITICAL.
	By(fmt.Sprintf("Waiting for deployments in namespace '%s' to be deleted", namespaceName))
	Eventually(func() (int, error) {
		err := k8sClient.List(ctx, deploymentList, client.InNamespace(namespaceName))
		if err != nil { return -1, err }
		return len(deploymentList.Items), nil
	}, time.Minute, time.Second).Should(BeZero(), "All deployments should be deleted")

	// Step 5: Now that the namespace is empty, delete it.
	By(fmt.Sprintf("Deleting test namespace '%s'", namespaceName))
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}}
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ns))).Should(Succeed())

	// Step 6: Wait for the namespace to be fully deleted. This will now succeed quickly.
	By(fmt.Sprintf("Waiting for namespace '%s' to be deleted", namespaceName))
	Eventually(func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: namespaceName}, ns)
		return errors.IsNotFound(err)
	}, time.Minute, time.Second).Should(BeTrue(), "The test namespace should be fully deleted")
}