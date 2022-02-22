IMAGE := znly/bazel-cache
VERSION := 0.1.3

.PHONY: bazel-cache
bazel-cache:
	go build -ldflags "-s -w" -trimpath -o $(@) .

.PHONY: image
image:
	docker buildx build --load --platform linux/amd64 -t $(IMAGE):$(VERSION) .
