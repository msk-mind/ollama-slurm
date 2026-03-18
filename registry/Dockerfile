FROM python:3.11-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY registry_server.py .
COPY dashboard.html .

RUN useradd -r -s /bin/false registry && \
    mkdir -p /data && \
    chown registry:registry /data

USER registry

ENV REGISTRY_FILE=/data/registry.json

EXPOSE 5000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:5000/health')"

ENTRYPOINT ["python3", "registry_server.py"]
CMD ["--host", "0.0.0.0", "--port", "5000"]
