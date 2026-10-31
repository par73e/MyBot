FROM python:3.12-slim
WORKDIR /srv/asrobot
COPY requirements.txt .
COPY wheels /wheels
RUN pip install --no-cache-dir --no-index --find-links=/wheels -r requirements.txt && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin asrobot
COPY app ./app
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 ASROBOT_DATA=/data ASROBOT_KEY_FILE=/run/secrets/master.key
USER 10001:10001
EXPOSE 18980
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "18980", "--no-access-log", "--proxy-headers", "--forwarded-allow-ips", "*"]
