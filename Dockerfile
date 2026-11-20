FROM python:3.12-slim
WORKDIR /app
COPY pyproject.toml README.md LICENSE ./
COPY atlas ./atlas
RUN pip install --no-cache-dir '.[drivers]' && useradd --create-home --uid 10001 atlas
USER atlas
WORKDIR /workspace
EXPOSE 8765
ENTRYPOINT ["atlas-schema-desk", "--config", "/workspace/atlas.toml", "--bind", "0.0.0.0"]
