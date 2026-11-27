# Common tasks. Postgres must be reachable at DATABASE_URL / TEST_DATABASE_URL.
TEST_DATABASE_URL ?= postgres://eimsky:eimsky@localhost:5432/eimsky_test?sslmode=disable

.PHONY: run seed build test test-go test-mobile e2e up down mobile web-mobile fmt

run: build            ## start the server on :8080
	./bin/eimsky serve

seed: build           ## wipe the database and load demo data
	./bin/eimsky seed --reset

build:
	go build -o bin/eimsky ./cmd/eimsky

test: test-go test-mobile

test-go:
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) go test -p 1 ./...

test-mobile:
	cd mobile && npx tsc --noEmit && npx jest

e2e:                  ## full browser + API lifecycle test
	E2E_DATABASE_URL=$(TEST_DATABASE_URL) ./e2e/run.sh

up:                   ## Postgres + app in Docker
	docker compose up --build -d && docker compose run --rm app seed --reset

down:
	docker compose down

mobile:               ## Expo dev server for the forwarder app
	cd mobile && npx expo start

fmt:
	gofmt -w cmd internal
