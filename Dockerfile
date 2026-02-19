FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETARCH=amd64

COPY binaries/yr-linux-${TARGETARCH} /app/yr

WORKDIR /app

EXPOSE 8080

CMD ["./yr"]
