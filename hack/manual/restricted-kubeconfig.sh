#!/usr/bin/env bash
# Writes a kubeconfig for a ServiceAccount that may list pods only in the
# "web" namespace (and may not list namespaces), for checking the namespace
# picker fallback and per-namespace Forbidden warnings by hand.
#
# Usage: hack/manual/restricted-kubeconfig.sh [out-file]   (default: ./restricted.kubeconfig)
set -euo pipefail

out=${1:-restricted.kubeconfig}
kubectl get ns web >/dev/null 2>&1 || kubectl create ns web
kubectl -n web create serviceaccount viewer --dry-run=client -o yaml | kubectl apply -f -
kubectl -n web create role pod-reader --verb=get,list,watch --resource=pods --dry-run=client -o yaml | kubectl apply -f -
kubectl -n web create rolebinding viewer-pods --role=pod-reader --serviceaccount=web:viewer --dry-run=client -o yaml | kubectl apply -f -

token=$(kubectl -n web create token viewer --duration=24h)
cluster=$(kubectl config view --minify -o jsonpath='{.clusters[0].name}')
server=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
ca=$(kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')

cat >"$out" <<YAML
apiVersion: v1
kind: Config
clusters:
- name: ${cluster}
  cluster: {server: ${server}, certificate-authority-data: ${ca}}
users:
- name: web-viewer
  user: {token: ${token}}
contexts:
- name: web-viewer
  context: {cluster: ${cluster}, user: web-viewer, namespace: web}
current-context: web-viewer
YAML
chmod 600 "$out"
echo "Wrote $out (context web-viewer, token valid for 24h). Add it as a source in KubeRoot."
