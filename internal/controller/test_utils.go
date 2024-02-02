package controller

import (
	"context"
	"time"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type TestClient struct {
	Timeout  time.Duration
	Interval time.Duration
	Ctx      context.Context
	client.Client
}

func (c *TestClient) MustCleanObjInNamespace(namespace string, obj client.Object) error {
	var err error
	err = nil
	Eventually(func() error {
		err = c.DeleteAllOf(c.Ctx, obj, client.InNamespace(namespace))
		return err
	}, c.Timeout, c.Interval).ShouldNot(HaveOccurred())
	return err
}

func (c *TestClient) RmNamespace(namespace string) error {
	var err error
	err = nil
	Eventually(func() error {
		err = c.Delete(c.Ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		})
		return err
	}, c.Timeout, c.Interval).ShouldNot(HaveOccurred())
	return err
}

func (c *TestClient) CreateNamespace(namespace string) error {
	var err error
	err = nil
	Eventually(func() error {
		err = c.Create(c.Ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		})
		return err
	}, c.Timeout, c.Interval).ShouldNot(HaveOccurred())
	return err
}

func (c *TestClient) CleanNamespace(namespace string) error {
	var err error
	err = nil
	if exist, err := c.NamespaceExists(namespace); err != nil {
		err = errors.Wrapf(err, "Failed to check if namespace exists: %s", namespace)
	} else if exist {
		err = c.RmNamespace(namespace)
	}

	if err != nil {
		return err
	}

	Eventually(func() error {
		err = c.CreateNamespace(namespace)
		return err
	}, c.Timeout, c.Interval).ShouldNot(HaveOccurred())

	return err
}

func (c *TestClient) NamespaceExists(namespace string) (bool, error) {
	ns := &corev1.Namespace{}
	err := c.Get(c.Ctx, types.NamespacedName{Name: namespace}, ns)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrapf(err, "Failed to get namespace: %s", namespace)
	}
	return true, nil

}
