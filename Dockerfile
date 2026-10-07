# dcrdata explorer image.
#
# The front end is plain CSS + native ES modules (no Node.js/npm/bundler), so
# there is nothing to build for the UI — the assets in cmd/dcrdata/public are
# served as-is. We just build the Go binary and copy it alongside the templates
# (views_v2/) and static assets (public/), which dcrdata serves relative to its
# working directory.

FROM golang:1.27.1-trixie AS build
COPY . /go/src
WORKDIR /go/src/cmd/dcrdata
RUN GOTOOLCHAIN=local go build -buildvcs=false -o /dcrdata .

FROM debian:trixie-slim
# ca-certificates is needed for outbound HTTPS (exchange and Politeia APIs).
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /dcrdata                      /app/dcrdata
COPY --from=build /go/src/cmd/dcrdata/views_v2  /app/views_v2
COPY --from=build /go/src/cmd/dcrdata/public    /app/public

EXPOSE 7777
# dcrdata's default apilisten is localhost:7777, which binds loopback inside
# the container and makes published ports unreachable; listen on all container
# interfaces by default (overridable at run time).
ENV DCRDATA_LISTEN_URL=0.0.0.0:7777
ENTRYPOINT ["/app/dcrdata"]
