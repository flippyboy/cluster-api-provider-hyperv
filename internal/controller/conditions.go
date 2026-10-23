package controller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
)

const requeueAfter = 15 * time.Second

func markReady(obj conditions.Setter, status metav1.ConditionStatus, reason, message string) {
	conditions.Set(obj, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func markPaused(obj conditions.Setter) {
	conditions.Set(obj, metav1.Condition{
		Type:    clusterv1.PausedCondition,
		Status:  metav1.ConditionTrue,
		Reason:  clusterv1.PausedReason,
		Message: "Reconciliation is paused",
	})
}

func clearPaused(obj conditions.Setter) {
	conditions.Delete(obj, clusterv1.PausedCondition)
}
