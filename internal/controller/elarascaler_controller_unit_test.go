// internal/controller/elarascaler_controller_unit_test.go
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Describe("ElaraScaler Unit Tests", func() {
	var reconciler ElaraScalerReconciler
	var logger = log.Log.WithName("unit-test")

	BeforeEach(func() {
		// We can use a mock reconciler for unit tests
		reconciler = ElaraScalerReconciler{}
	})

	Context("applyConstraintsAndRedistribute", func() {

		It("should redistribute surplus replicas during scale-up", func() {
			// Scenario: We want to add 6 replicas total.
			// dep1 is almost full and can only take 2.
			// dep2 has plenty of room.
			// The surplus of 2 from dep1 should be given to dep2.
			entities := &scalingEntities{
				Deployments: map[types.NamespacedName]*deploymentInfo{
					{Name: "dep1", Namespace: "ns"}: {Min: 1, Max: 10, CurrentReplicas: 8},
					{Name: "dep2", Namespace: "ns"}: {Min: 1, Max: 10, CurrentReplicas: 2},
				},
			}
			desiredChanges := map[types.NamespacedName]int{
				{Name: "dep1", Namespace: "ns"}: 4, // Wants +4, but can only take +2
				{Name: "dep2", Namespace: "ns"}: 2, // Wants +2
			}

			finalChanges := reconciler.applyConstraintsAndRedistribute(logger, entities, desiredChanges)

			// dep1 takes +2 (hits its max of 10).
			// dep2 takes its desired +2, PLUS the surplus +2 from dep1.
			Expect(finalChanges).To(HaveKeyWithValue(types.NamespacedName{Name: "dep1", Namespace: "ns"}, 2))
			Expect(finalChanges).To(HaveKeyWithValue(types.NamespacedName{Name: "dep2", Namespace: "ns"}, 4))
		})

		It("should redistribute deficit replicas during scale-down", func() {
			// Scenario: We want to remove 5 replicas total.
			// dep1 is almost at its minimum and can only give up 1.
			// dep2 has plenty of replicas to remove.
			// The deficit of 2 from dep1 should be taken from dep2.
			entities := &scalingEntities{
				Deployments: map[types.NamespacedName]*deploymentInfo{
					{Name: "dep1", Namespace: "ns"}: {Min: 2, Max: 10, CurrentReplicas: 3},
					{Name: "dep2", Namespace: "ns"}: {Min: 2, Max: 10, CurrentReplicas: 9},
				},
			}
			desiredChanges := map[types.NamespacedName]int{
				{Name: "dep1", Namespace: "ns"}: -3, // Wants -3, but can only give -1
				{Name: "dep2", Namespace: "ns"}: -2, // Wants -2
			}

			finalChanges := reconciler.applyConstraintsAndRedistribute(logger, entities, desiredChanges)

			// dep1 gives -1 (hits its min of 2).
			// dep2 gives its desired -2, PLUS the deficit -2 from dep1.
			Expect(finalChanges).To(HaveKeyWithValue(types.NamespacedName{Name: "dep1", Namespace: "ns"}, -1))
			Expect(finalChanges).To(HaveKeyWithValue(types.NamespacedName{Name: "dep2", Namespace: "ns"}, -4))
		})
	})
})
