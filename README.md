Install
=======

```
skaffold config set --global local-cluster true
minikube addons enable ingress
curl -sL run.linkerd.io/install | sh
linkerd check --pre                     # validate that Linkerd can be installed
linkerd install --crds | kubectl apply -f - # install the Linkerd CRDs
linkerd --set proxyInit.runAsRoot=true install | kubectl apply -f -    # install the control plane into the 'linkerd' namespace
linkerd check                           # validate everything worked!
kubectl create namespace k8s-experiment -o yaml | linkerd inject -|kubectl apply -f -
linkerd jaeger install | kubectl apply -f -
linkerd jaeger check

kubectl create namespace nats
```

Test
====

```
curl -XPOST -d '{"id": "user1"}' -H 'Content-Type: application/json' -v http://minikube.ingress/users
curl -XPOST -d '{"id": "company1"}' -H 'Content-Type: application/json' -v http://minikube.ingress/companies

curl -H 'Content-Type: application/json' -v http://minikube.ingress/users/user1
curl -H 'Content-Type: application/json' -v http://minikube.ingress/companies/company1
```
