# Runtime-only deployment of a tested Linux binary; avoids compilation on the VPS.
FROM gcr.io/distroless/static-debian12:nonroot
COPY watcher /watcher
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/watcher"]
