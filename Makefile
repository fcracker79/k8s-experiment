build-rest:
	docker build -t fcracker79/k8s-experiment-company docker/rest/company

build-images: build-rest
