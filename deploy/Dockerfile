# Runtime-only deployment of a tested Linux binary; avoids compilation on the VPS.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY watcher /watcher
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/watcher"]
