build-rest:
	docker build -t fcracker79/k8s-experiment-company:0.0.1 docker/rest/company

build-grpc:
	docker build -t fcracker79/k8s-experiment-user:0.0.1 docker/grpc/user

build-apigw:
	docker build -t fcracker79/k8s-experiment-apigw:0.0.1 docker/apigw

build-async-user:
	docker build -t fcracker79/k8s-experiment-apigw:0.0.1 docker/nats/async_users

build-images: build-rest build-grpc build-apigw build-async-user

deploy-grpc: build-grpc
	docker push fcracker79/k8s-experiment-user:0.0.1

deploy-rest: build-rest
	docker push fcracker79/k8s-experiment-company:0.0.1

deploy-apigw: build-apigw
	docker push fcracker79/k8s-experiment-apigw:0.0.1

deploy-async-user: build-async-user
	docker push fcracker79/k8s-experiment-async-user:0.0.1

deploy-images: deploy-rest deploy-grpc deploy-apigw deploy-async-user

install-chart:
	helm upgrade test-release helm --namespace test --install

package-chart:
	helm package helm -d helm/artifacthub/package
	helm repo index helm
	mv helm/index.yaml helm/artifacthub

run:
	cd skaffold && skaffold run --namespace=k8s-experiment

dev:
	cd skaffold && skaffold dev --namespace=k8s-experiment

stop:
	cd skaffold && skaffold delete --namespace=k8s-experiment
