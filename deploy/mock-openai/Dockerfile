FROM alpine:3.22
RUN apk add --no-cache python3 \
    && addgroup -S -g 10002 mockopenai \
    && adduser -S -D -H -u 10002 -G mockopenai mockopenai
WORKDIR /app
COPY --chown=10002:10002 deploy/mock-openai/server.py /app/server.py
USER 10002:10002
EXPOSE 8090
ENTRYPOINT ["python3", "/app/server.py"]
