#!/bin/bash

set -e

curl -XPOST -d '{"id": "user1"}' -H 'Content-Type: application/json' -v http://minikube.ingress/users
curl -XPOST -d '{"id": "company1"}' -H 'Content-Type: application/json' -v http://minikube.ingress/companies

while [[ true ]]; do
	curl -H 'Content-Type: application/json' -v http://minikube.ingress/users/user1
	curl -H 'Content-Type: application/json' -v http://minikube.ingress/companies/company1
done
