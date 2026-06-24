# syntax=docker/dockerfile:1
#
# Multi-arch container image for the crond-agent CLI.
#
# Goreleaser builds the static binary (per agent/.goreleaser.yaml) and passes
# it into this build context. This Dockerfile does NOT compile — it only
# copies the pre-built binary onto a minimal scratch base alongside CA certs
# so the agent can do HTTPS to crond.io.
#
# Image is intentionally tiny (~10MB) to fit the K8s init-container copy
# pattern documented in deploy/k8s/helm/crond-agent/.

# Pinned by digest so a base-image republish can't silently change the
# certs layer. Refresh via `docker buildx imagetools inspect alpine:3.20`
# when bumping the minor.
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
ARG TARGETPLATFORM
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# Goreleaser dockers_v2 places each platform's binary under a TARGETPLATFORM-
# prefixed subdirectory of the build context (e.g. linux/amd64/crond-agent).
# Using the ARG lets one Dockerfile serve every arch in a single buildx run.
COPY $TARGETPLATFORM/crond-agent /crond-agent
ENTRYPOINT ["/crond-agent"]
