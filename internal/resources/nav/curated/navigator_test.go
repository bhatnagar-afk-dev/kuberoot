package curated

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"kuberoot.in/kuberoot/internal/resources"
)

var update = flag.Bool("update", false, "rewrite golden files")

func rt(group, version, resource, kind string, namespaced bool) resources.ResourceType {
	return resources.ResourceType{Group: group, Version: version, Resource: resource, Kind: kind, Namespaced: namespaced}
}

// kindLike is a subset of what a kind cluster serves, plus a few CRDs, in
// discovery order (sorted by group and resource).
var kindLike = []resources.ResourceType{
	rt("", "v1", "configmaps", "ConfigMap", true),
	rt("", "v1", "endpoints", "Endpoints", true),
	rt("", "v1", "events", "Event", true),
	rt("", "v1", "namespaces", "Namespace", false),
	rt("", "v1", "nodes", "Node", false),
	rt("", "v1", "pods", "Pod", true),
	rt("", "v1", "podtemplates", "PodTemplate", true),
	rt("", "v1", "secrets", "Secret", true),
	rt("", "v1", "services", "Service", true),
	rt("apps", "v1", "deployments", "Deployment", true),
	rt("apps", "v1", "replicasets", "ReplicaSet", true),
	rt("argoproj.io", "v1alpha1", "applications", "Application", true),
	rt("argoproj.io", "v1alpha1", "appprojects", "AppProject", true),
	rt("batch", "v1", "jobs", "Job", true),
	rt("cert-manager.io", "v1", "certificates", "Certificate", true),
	rt("cluster.x-k8s.io", "v1beta1", "clusters", "Cluster", true),
	rt("events.k8s.io", "v1", "events", "Event", true),
	rt("networking.k8s.io", "v1", "ingresses", "Ingress", true),
	rt("networking.k8s.io", "v1", "networkpolicies", "NetworkPolicy", true),
	rt("rbac.authorization.k8s.io", "v1", "clusterroles", "ClusterRole", false),
	rt("snapshot.storage.k8s.io", "v1", "volumesnapshots", "VolumeSnapshot", true),
}

func TestNavigationGolden(t *testing.T) {
	got, err := json.MarshalIndent(Navigator{}.Navigation(kindLike), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "navigation.golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("navigation differs from %s (run with -update to accept):\n%s", path, got)
	}
}

func TestNavigationEmpty(t *testing.T) {
	if got := (Navigator{}).Navigation(nil); len(got) != 0 {
		t.Errorf("Navigation(nil) = %v, want no groups", got)
	}
}

func TestEverythingServedIsListed(t *testing.T) {
	seen := map[resources.ResourceType]int{}
	var walk func([]resources.NavItem)
	walk = func(items []resources.NavItem) {
		for _, it := range items {
			if it.Type != nil {
				seen[*it.Type]++
			}
			walk(it.Items)
		}
	}
	for _, g := range (Navigator{}).Navigation(kindLike) {
		walk(g.Items)
	}
	for _, typ := range kindLike {
		if seen[typ] != 1 {
			t.Errorf("%s/%s listed %d times, want 1", typ.Group, typ.Resource, seen[typ])
		}
	}
}

func TestTitle(t *testing.T) {
	for _, tc := range []struct {
		resource, kind, want string
	}{
		{"pods", "Pod", "Pods"},
		{"ingresses", "Ingress", "Ingresses"},
		{"networkpolicies", "NetworkPolicy", "NetworkPolicies"},
		{"endpoints", "Endpoints", "Endpoints"},
		{"pods", "PodMetrics", "PodMetrics"},
		{"things", "", "things"},
	} {
		if got := Title(rt("g", "v1", tc.resource, tc.kind, true)); got != tc.want {
			t.Errorf("Title(%s, %s) = %q, want %q", tc.resource, tc.kind, got, tc.want)
		}
	}
}
