package controller

import (
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func isAnnotated() predicate.Funcs {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		sts, ok := obj.(*appsv1.StatefulSet)
		if !ok {
			return false
		}
		// Check if the annotation exists
		_, exists := sts.Annotations[annotation]
		return exists
	})
}
