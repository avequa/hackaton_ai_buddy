# Агент AI IT-Buddy (LangGraph): один процесс gunicorn - треды диалогов хранятся в памяти процесса
FROM python:3.13-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1
WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt
COPY buddy_agent ./buddy_agent
RUN useradd --system --uid 10001 agent
USER 10001
EXPOSE 8000
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
  CMD ["python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8000/healthz', timeout=3)"]
CMD ["gunicorn", "-b", "0.0.0.0:8000", "-w", "1", "--threads", "8", "--timeout", "180", "--access-logfile", "-", "buddy_agent.app:create_app()"]
