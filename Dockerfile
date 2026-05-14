# syntax = docker/dockerfile-upstream:1.23.0-labs

# THIS FILE WAS AUTOMATICALLY GENERATED, PLEASE DO NOT EDIT.
#
# Generated on 2026-05-14T21:19:16Z by kres 1762ab2.

ARG TOOLCHAIN=scratch

# helm toolchain
FROM --platform=${BUILDPLATFORM} ${TOOLCHAIN} AS helm-toolchain
ARG HELMDOCS_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go install github.com/norwoodj/helm-docs/cmd/helm-docs@${HELMDOCS_VERSION} \
	&& mv /go/bin/helm-docs /bin/helm-docs

FROM ghcr.io/siderolabs/ca-certificates:v1.13.0 AS image-ca-certificates

FROM ghcr.io/siderolabs/fhs:v1.13.0 AS image-fhs

# runs markdownlint
FROM docker.io/oven/bun:1.3.13-alpine AS lint-markdown
WORKDIR /src
RUN bun i markdownlint-cli@0.48.0 sentences-per-line@0.5.2
COPY .markdownlint.json .
COPY ./README.md ./README.md
RUN bunx markdownlint --ignore "CHANGELOG.md" --ignore "**/node_modules/**" --ignore '**/hack/chglog/**' --rules markdownlint-sentences-per-line .

# copies the runner hooks
FROM scratch AS runner-hooks
COPY --link --chmod=0755 hack/runner-hooks/job-started.sh /runner-hooks/job-started.sh
COPY --link --chmod=0755 hack/runner-hooks/job-completed.sh /runner-hooks/job-completed.sh

# base toolchain image
FROM --platform=${BUILDPLATFORM} ${TOOLCHAIN} AS toolchain
RUN apk --update --no-cache add bash build-base curl jq protoc protobuf-dev

# runs helm-docs
FROM helm-toolchain AS helm-docs-run
WORKDIR /src
COPY deploy/helm/buildkit-cache-controller /src/deploy/helm/buildkit-cache-controller
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/root/.cache/helm-docs,id=buildkit-cache-controller/root/.cache/helm-docs,sharing=locked helm-docs --badge-style=flat

# build tools
FROM --platform=${BUILDPLATFORM} toolchain AS tools
ENV GO111MODULE=on
ARG CGO_ENABLED
ENV CGO_ENABLED=${CGO_ENABLED}
ARG GOTOOLCHAIN
ENV GOTOOLCHAIN=${GOTOOLCHAIN}
ARG GOEXPERIMENT
ENV GOEXPERIMENT=${GOEXPERIMENT}
ENV GOPATH=/go
ARG GOIMPORTS_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go install golang.org/x/tools/cmd/goimports@v${GOIMPORTS_VERSION}
RUN mv /go/bin/goimports /bin
ARG GOMOCK_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go install go.uber.org/mock/mockgen@v${GOMOCK_VERSION}
RUN mv /go/bin/mockgen /bin
ARG DEEPCOPY_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go install github.com/siderolabs/deep-copy@${DEEPCOPY_VERSION} \
	&& mv /go/bin/deep-copy /bin/deep-copy
ARG GOLANGCILINT_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCILINT_VERSION} \
	&& mv /go/bin/golangci-lint /bin/golangci-lint
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go install golang.org/x/vuln/cmd/govulncheck@latest \
	&& mv /go/bin/govulncheck /bin/govulncheck
ARG DIS_VULNCHECK_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go install github.com/shanduur/dis-vulncheck@${DIS_VULNCHECK_VERSION} \
	&& mv /go/bin/dis-vulncheck /bin/dis-vulncheck
ARG GOFUMPT_VERSION
RUN go install mvdan.cc/gofumpt@${GOFUMPT_VERSION} \
	&& mv /go/bin/gofumpt /bin/gofumpt

# clean helm-docs output
FROM scratch AS helm-docs
COPY --from=helm-docs-run /src/deploy/helm/buildkit-cache-controller deploy/helm/buildkit-cache-controller

# tools and sources
FROM tools AS base
WORKDIR /src
COPY go.mod go.mod
COPY go.sum go.sum
RUN cd .
RUN --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go mod download
RUN --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go mod verify
COPY ./api ./api
COPY ./cmd ./cmd
COPY ./internal ./internal
RUN --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go list -mod=readonly all >/dev/null

FROM tools AS embed-generate
ARG SHA
ARG TAG
WORKDIR /src
RUN mkdir -p internal/version/data && \
    echo -n ${SHA} > internal/version/data/sha && \
    echo -n ${TAG} > internal/version/data/tag

# run go generate
FROM base AS go-generate-0
WORKDIR /src
COPY .license-header.go.txt hack/.license-header.go.txt
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg go generate ./api/...
RUN goimports -w -local github.com/siderolabs/buildkit-cache-controller ./api

# runs gofumpt
FROM base AS lint-gofumpt
RUN FILES="$(gofumpt -l .)" && test -z "${FILES}" || (echo -e "Source code is not formatted with 'gofumpt -w .':\n${FILES}"; exit 1)

# runs golangci-lint
FROM base AS lint-golangci-lint
WORKDIR /src
COPY .golangci.yml .
ENV GOGC=50
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/root/.cache/golangci-lint,id=buildkit-cache-controller/root/.cache/golangci-lint,sharing=locked --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg golangci-lint run --config .golangci.yml

# runs golangci-lint fmt
FROM base AS lint-golangci-lint-fmt-run
WORKDIR /src
COPY .golangci.yml .
ENV GOGC=50
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/root/.cache/golangci-lint,id=buildkit-cache-controller/root/.cache/golangci-lint,sharing=locked --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg golangci-lint fmt --config .golangci.yml
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/root/.cache/golangci-lint,id=buildkit-cache-controller/root/.cache/golangci-lint,sharing=locked --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg golangci-lint run --fix --issues-exit-code 0 --config .golangci.yml

# runs govulncheck
FROM base AS lint-govulncheck
WORKDIR /src
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg dis-vulncheck -tool=false ./...

# runs unit-tests with race detector
FROM base AS unit-tests-race
WORKDIR /src
ARG TESTPKGS
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg --mount=type=cache,target=/tmp,id=buildkit-cache-controller/tmp CGO_ENABLED=1 go test -race ${TESTPKGS}

# runs unit-tests
FROM base AS unit-tests-run
WORKDIR /src
ARG TESTPKGS
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg --mount=type=cache,target=/tmp,id=buildkit-cache-controller/tmp go test -covermode=atomic -coverprofile=coverage.txt -coverpkg=${TESTPKGS} ${TESTPKGS}

FROM embed-generate AS embed-abbrev-generate
WORKDIR /src
ARG ABBREV_TAG
RUN echo -n 'undefined' > internal/version/data/sha && \
    echo -n ${ABBREV_TAG} > internal/version/data/tag

# clean golangci-lint fmt output
FROM scratch AS lint-golangci-lint-fmt
COPY --from=lint-golangci-lint-fmt-run /src .

FROM scratch AS unit-tests
COPY --from=unit-tests-run /src/coverage.txt /coverage-unit-tests.txt

# cleaned up specs and compiled versions
FROM scratch AS generate
COPY --from=go-generate-0 /src/api/v1alpha1/zz_generated.deepcopy.go api/v1alpha1/zz_generated.deepcopy.go
COPY --from=go-generate-0 /src/deploy/helm/buildkit-cache-controller/crds/ci.siderolabs.com_cachedbuilds.yaml deploy/helm/buildkit-cache-controller/crds/ci.siderolabs.com_cachedbuilds.yaml
COPY --from=go-generate-0 /src/deploy/helm/buildkit-cache-controller/crds/ci.siderolabs.com_cachedbuildtiers.yaml deploy/helm/buildkit-cache-controller/crds/ci.siderolabs.com_cachedbuildtiers.yaml
COPY --from=embed-abbrev-generate /src/internal/version internal/version

# builds buildkit-cache-controller-linux-amd64
FROM base AS buildkit-cache-controller-linux-amd64-build
COPY --from=generate / /
COPY --from=embed-generate / /
WORKDIR /src/cmd/buildkit-cache-controller
ARG GO_BUILDFLAGS
ARG GO_LDFLAGS
ARG VERSION_PKG="internal/version"
ARG SHA
ARG TAG
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg GOARCH=amd64 GOOS=linux go build ${GO_BUILDFLAGS} -ldflags "${GO_LDFLAGS} -X ${VERSION_PKG}.Name=buildkit-cache-controller -X ${VERSION_PKG}.SHA=${SHA} -X ${VERSION_PKG}.Tag=${TAG}" -o /buildkit-cache-controller-linux-amd64

# builds buildkit-cache-controller-linux-arm64
FROM base AS buildkit-cache-controller-linux-arm64-build
COPY --from=generate / /
COPY --from=embed-generate / /
WORKDIR /src/cmd/buildkit-cache-controller
ARG GO_BUILDFLAGS
ARG GO_LDFLAGS
ARG VERSION_PKG="internal/version"
ARG SHA
ARG TAG
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg GOARCH=arm64 GOOS=linux go build ${GO_BUILDFLAGS} -ldflags "${GO_LDFLAGS} -X ${VERSION_PKG}.Name=buildkit-cache-controller -X ${VERSION_PKG}.SHA=${SHA} -X ${VERSION_PKG}.Tag=${TAG}" -o /buildkit-cache-controller-linux-arm64

# builds cb-hook-linux-amd64
FROM base AS cb-hook-linux-amd64-build
COPY --from=generate / /
COPY --from=embed-generate / /
WORKDIR /src/cmd/cb-hook
ARG GO_BUILDFLAGS
ARG GO_LDFLAGS
ARG VERSION_PKG="internal/version"
ARG SHA
ARG TAG
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg GOARCH=amd64 GOOS=linux go build ${GO_BUILDFLAGS} -ldflags "${GO_LDFLAGS} -X ${VERSION_PKG}.Name=cb-hook -X ${VERSION_PKG}.SHA=${SHA} -X ${VERSION_PKG}.Tag=${TAG}" -o /cb-hook-linux-amd64

# builds cb-hook-linux-arm64
FROM base AS cb-hook-linux-arm64-build
COPY --from=generate / /
COPY --from=embed-generate / /
WORKDIR /src/cmd/cb-hook
ARG GO_BUILDFLAGS
ARG GO_LDFLAGS
ARG VERSION_PKG="internal/version"
ARG SHA
ARG TAG
RUN --mount=type=cache,target=/root/.cache/go-build,id=buildkit-cache-controller/root/.cache/go-build --mount=type=cache,target=/go/pkg,id=buildkit-cache-controller/go/pkg GOARCH=arm64 GOOS=linux go build ${GO_BUILDFLAGS} -ldflags "${GO_LDFLAGS} -X ${VERSION_PKG}.Name=cb-hook -X ${VERSION_PKG}.SHA=${SHA} -X ${VERSION_PKG}.Tag=${TAG}" -o /cb-hook-linux-arm64

FROM scratch AS buildkit-cache-controller-linux-amd64
COPY --from=buildkit-cache-controller-linux-amd64-build /buildkit-cache-controller-linux-amd64 /buildkit-cache-controller-linux-amd64

FROM scratch AS buildkit-cache-controller-linux-arm64
COPY --from=buildkit-cache-controller-linux-arm64-build /buildkit-cache-controller-linux-arm64 /buildkit-cache-controller-linux-arm64

FROM scratch AS cb-hook-linux-amd64
COPY --from=cb-hook-linux-amd64-build /cb-hook-linux-amd64 /cb-hook-linux-amd64

FROM scratch AS cb-hook-linux-arm64
COPY --from=cb-hook-linux-arm64-build /cb-hook-linux-arm64 /cb-hook-linux-arm64

FROM buildkit-cache-controller-linux-${TARGETARCH} AS buildkit-cache-controller

FROM scratch AS buildkit-cache-controller-all
COPY --from=buildkit-cache-controller-linux-amd64 / /
COPY --from=buildkit-cache-controller-linux-arm64 / /

FROM cb-hook-linux-${TARGETARCH} AS cb-hook

FROM scratch AS cb-hook-all
COPY --from=cb-hook-linux-amd64 / /
COPY --from=cb-hook-linux-arm64 / /

FROM scratch AS image-buildkit-cache-controller
ARG TARGETARCH
COPY --from=buildkit-cache-controller buildkit-cache-controller-linux-${TARGETARCH} /usr/local/bin/buildkit-cache-controller
COPY --from=image-fhs / /
COPY --from=image-ca-certificates / /
LABEL org.opencontainers.image.source=https://github.com/siderolabs/buildkit-cache-controller
ENTRYPOINT ["/usr/local/bin/buildkit-cache-controller"]

FROM scratch AS image-cb-hook
ARG TARGETARCH
COPY --from=cb-hook cb-hook-linux-${TARGETARCH} /cb-hook
COPY --from=image-fhs / /
COPY --from=image-ca-certificates / /
COPY --from=runner-hooks / /
LABEL org.opencontainers.image.source=https://github.com/siderolabs/buildkit-cache-controller
ENTRYPOINT ["/cb-hook"]

