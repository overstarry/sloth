.PHONY: build run test vet fmt sqlc tidy migrate-up migrate-down

GOBIN := $(shell go env GOPATH)/bin

build:
	go build -o bin/sloth ./cmd/sloth

run: build
	./bin/sloth -config config.yaml

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Regenerate type-safe store code after editing schema.sql or queries/*.sql.
sqlc:
	$(GOBIN)/sqlc generate

# Verify committed generated code matches the SQL (run in CI).
sqlc-verify:
	$(GOBIN)/sqlc diff

tidy:
	go mod tidy

# Requires golang-migrate: go install -tags 'postgres' \
#   github.com/golang-migrate/migrate/v4/cmd/migrate@latest
migrate-up:
	$(GOBIN)/migrate -path internal/store/migrations -database "$(SLOTH_STORE_DSN)" up

migrate-down:
	$(GOBIN)/migrate -path internal/store/migrations -database "$(SLOTH_STORE_DSN)" down 1
