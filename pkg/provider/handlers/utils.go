package handlers

import (
	"context"
	"net/http"
	"path"

	"github.com/faasedge/core/pkg"
	core "github.com/faasedge/core/pkg"
	provider "github.com/faasedge/core/pkg/provider"
)

func getRequestNamespace(namespace string) string {

	if len(namespace) > 0 {
		return namespace
	}
	return core.DefaultFunctionNamespace
}

func readNamespaceFromQuery(r *http.Request) string {
	q := r.URL.Query()
	return q.Get("namespace")
}

func getNamespaceSecretMountPath(userSecretPath string, namespace string) string {
	return path.Join(userSecretPath, namespace)
}

// validNamespace indicates whether the namespace is eligable to be
// used for OpenFaaS functions.
func validNamespace(store provider.Labeller, namespace string) (bool, error) {
	if namespace == core.DefaultFunctionNamespace {
		return true, nil
	}

	labels, err := store.Labels(context.Background(), namespace)
	if err != nil {
		return false, err
	}

	if value, found := labels[pkg.NamespaceLabel]; found && value == "true" {
		return true, nil
	}

	return false, nil
}
