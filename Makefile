build-rest:
	docker build -t fcracker79/k8s-experiment-company docker/rest/company

build-grpc:
	docker build -t fcracker79/k8s-experiment-user docker/grpc/user

build-images: build-rest build-grpc
