FROM python:3.12-slim-bookworm
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir --requirement requirements.txt
COPY handler.py app.py storage.py ./
COPY runtime ./runtime
ENV PORT=8080 PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
EXPOSE 8080
LABEL com.gregale.secret-reload-signal="SIGHUP" \
      com.gregale.secret-reload-readiness="required"
USER 1000:1000
CMD ["python", "handler.py"]
