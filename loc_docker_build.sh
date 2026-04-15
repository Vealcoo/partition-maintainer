#!/bin/bash
set -euo pipefail

TARGET="${1:-all}"
TAG="${2:-latest}"
REGISTRY="${3:-}"

if [[ -z "${REGISTRY}" ]]; then
	echo "Usage: $0 [main|just-ping|all] [tag] <registry>"
	exit 1
fi

MAIN_IMAGE="partition-maintainer:${TAG}"
MAIN_REMOTE_IMAGE="${REGISTRY}/partition-maintainer:${TAG}"
PING_IMAGE="partition-maintainer-just-ping:${TAG}"
PING_REMOTE_IMAGE="${REGISTRY}/partition-maintainer-just-ping:${TAG}"

build_main() {
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ./app_exec ./cmd/partition-maintainer
	docker build --platform linux/amd64 -f Dockerfile.loc --build-arg APP_BINARY=app_exec -t "${MAIN_IMAGE}" .
	docker tag "${MAIN_IMAGE}" "${MAIN_REMOTE_IMAGE}"
	docker push "${MAIN_REMOTE_IMAGE}"
}

build_just_ping() {
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ./app_exec_just_ping ./cmd/just-ping
	docker build --platform linux/amd64 -f Dockerfile.loc --build-arg APP_BINARY=app_exec_just_ping -t "${PING_IMAGE}" .
	docker tag "${PING_IMAGE}" "${PING_REMOTE_IMAGE}"
	docker push "${PING_REMOTE_IMAGE}"
}

case "${TARGET}" in
	main)
		build_main
		;;
	just-ping)
		build_just_ping
		;;
	all)
		build_main
		build_just_ping
		;;
	*)
		echo "Usage: $0 [main|just-ping|all] [tag] <registry>"
		exit 1
		;;
esac
