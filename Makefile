.PHONY: fmt check test test-integration build docker-build test-containers compare-runtime test-deploy test-production-compose
fmt:
	gofmt -w cmd internal migrations
check:
	@test -z "$$(gofmt -l cmd internal migrations)" || (gofmt -l cmd internal migrations; exit 1)
	go vet ./...
	go test -race ./...
test:
	go test ./...
test-integration:
	@test -n "$$TEST_DATABASE_URL" || (echo 'Set TEST_DATABASE_URL to an isolated PostgreSQL'; exit 1)
	go test -race -tags integration ./...
build:
	go build -trimpath -o bin/dota-doggo ./cmd/dota-doggo
docker-build:
	docker build -t dota-doggo:local .
test-containers: docker-build
	docker build --target fixture-api -t dota-doggo:fixture-api .
	python3 scripts/smoke_containers.py
compare-runtime: docker-build
	docker build --target fixture-api -t dota-doggo:fixture-api .
	python3 scripts/compare_runtime.py
test-deploy:
	bash -n deploy/deploy.sh scripts/deploy_vps.sh scripts/test_deploy.sh
	docker run --rm --network none --entrypoint bash \
	  --mount type=bind,source="$(CURDIR)/deploy",target=/src/deploy,readonly \
	  --mount type=bind,source="$(CURDIR)/scripts",target=/src/scripts,readonly \
	  postgres:17-alpine /src/scripts/test_deploy.sh
test-production-compose: test-containers
	python3 scripts/smoke_production.py
