build-rest:
	docker build -t fcracker79/k8s-experiment-company:0.0.1 docker/rest/company

build-grpc:
	docker build -t fcracker79/k8s-experiment-user:0.0.1 docker/grpc/user

build-images: build-rest build-grpc

deploy-grpc: build-grpc
	docker push fcracker79/k8s-experiment-user:0.0.1

deploy-rest: build-rest
	docker push fcracker79/k8s-experiment-company:0.0.1

deploy-images: deploy-rest deploy-grpc

install-chart:
	helm upgrade test-release helm --namespace test --install

package-chart:
	helm package helm -d helm/artifacthub/package
	helm repo index helm
	mv helm/index.yaml helm/artifacthub
