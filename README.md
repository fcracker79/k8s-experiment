Install
=======

```
skaffold config set --global local-cluster true
minikube addons enable ingress

curl -sL run.linkerd.io/install | sh
or
curl --proto '=https' --tlsv1.2 -sSfL https://run.linkerd.io/install-edge | sh

linkerd check --pre                     # validate that Linkerd can be installed
linkerd install --crds | kubectl apply -f - # install the Linkerd CRDs
linkerd --set proxyInit.runAsRoot=true install | kubectl apply -f -    # install the control plane into the 'linkerd' namespace
linkerd check                           # validate everything worked!
kubectl create namespace k8s-experiment -o yaml | linkerd inject -|kubectl apply -f -
linkerd jaeger install | kubectl apply -f -
linkerd jaeger check

kubectl create namespace nats
kubectl create namespace prometheus
kubectl create namespace grafana
```

Deploy
=======

1. Deploy the artifacts: `make run`
2. Expose the Prometheus server: `kubectl expose service kube-prometheus-stack-prometheus --namespace prometheus --type=NodePort --target-port=9090 --name=prometheus-server-np`
3. Get the minikube URL for the exposed server: `minikube service prometheus-server-np --url --namespace prometheus`
4. Expose the Grafana server: `kubectl expose service grafana --type=NodePort --target-port=3000 --name=grafana-np --namespace grafana`
5. Get the minikube URL for the exposed server: `minikube service grafana-np --url --namespace grafana`
6. Get the password to access grafana: `kubectl get secret --namespace grafana grafana -o jsonpath="{.data.admin-password}" | base64 --decode ; echo`
7. Access grafana using the `admin` account and the password found at previous point
4. Expose the Grafana server: `kubectl expose service grafana --type=NodePort --target-port=3000 --name=grafana-np --namespace grafana`
5. Get the minikube URL for the exposed server: `minikube service grafana-np --url --namespace grafana`
6. Get the password to access grafana: `kubectl get secret --namespace grafana grafana -o jsonpath="{.data.admin-password}" | base64 --decode ; echo`
7. Access grafana using the `admin` account and the password found at previous point
8. Install Prometheus datasource to Grafana, URL `http://kube-prometheus-stack-prometheus.prometheus.svc.cluster.local:9090`
9. Import dashboards

Test
====

```
curl -XPOST -d '{"id": "user1"}' -H 'Content-Type: application/json' -v http://minikube.ingress/users
curl -XPOST -d '{"id": "company1"}' -H 'Content-Type: application/json' -v http://minikube.ingress/companies

curl -H 'Content-Type: application/json' -v http://minikube.ingress/users/user1
curl -H 'Content-Type: application/json' -v http://minikube.ingress/companies/company1
```

For async:

1. On NATS box:
   `nats -s nats.nats.svc.cluster.local:4222 subscribe 'k8s.experiment.users.>'`
2. `curl -XPOST -d '{"id": "user1"}' -H 'Content-Type: application/json' -v http://minikube.ingress/async/users`

Notes
=====
Jaeger dashboard shows `linkerd-proxy` as service name.
This problem is addressed at https://github.com/linkerd/linkerd2/issues/11157 and, at the time of writing, is still open.
