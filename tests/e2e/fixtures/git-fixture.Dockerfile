FROM alpine:3.20
RUN apk add --no-cache git busybox-extras
COPY docker-build-repo /repo-src/docker-build-repo
COPY docker-build-repo-broken /repo-src/docker-build-repo-broken
COPY git-fixture-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 80
ENTRYPOINT ["/entrypoint.sh"]
