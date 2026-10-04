# Pinned linux/amd64 static image; includes TLS roots and the nonroot user.
FROM gcr.io/distroless/static-debian13@sha256:2293b36c7c9082bf4115aab724b4d2cddec82c8eba39bf27ac0517e159acf150
COPY --chown=65532:65532 _output/provider /provider
COPY --chown=65532:65532 LICENSE THIRD_PARTY_LICENSES.md /licenses/
USER 65532:65532
ENTRYPOINT ["/provider"]
