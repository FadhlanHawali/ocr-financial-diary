FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

# ==========================
# System dependencies (IMPORTANT for OpenCV + PaddleOCR)
# ==========================
RUN apt-get update && apt-get install -y --no-install-recommends \
    libgl1 \
    libglib2.0-0 \
    libsm6 \
    libxext6 \
    libxrender1 \
    libx11-6 \
    libxcb1 \
    gcc \
    && rm -rf /var/lib/apt/lists/*

# ==========================
# Create app directory
# ==========================
WORKDIR /app

# ==========================
# Upgrade pip
# ==========================
RUN pip install --upgrade pip

# ==========================
# Install PaddlePaddle (CPU version)
# ==========================
RUN pip install --no-cache-dir \
    paddlepaddle==3.2.0 \
    -i https://www.paddlepaddle.org.cn/packages/stable/cpu/

# ==========================
# Install Python dependencies
# ==========================
COPY requirements.txt .

RUN pip install --no-cache-dir -r requirements.txt

# ==========================
# Copy application code
# ==========================
COPY . .
 
# ==========================
# Run as non-root user (optional but recommended)
# ==========================
# Fixed UID/GID, and USER by number: Docker then never needs to look the
# name up in /etc/passwd when starting the container.
RUN groupadd -g 1000 appuser \
    && useradd -m -u 1000 -g 1000 appuser \
    && mkdir -p /home/appuser/.paddlex/official_models \
    && chown -R 1000:1000 /app /home/appuser
USER 1000:1000

# ==========================
# Expose port
# ==========================
EXPOSE 8001

# ==========================
# Start server
# ==========================
CMD ["gunicorn", \
     "-k", "uvicorn.workers.UvicornWorker", \
     "--workers", "4", \
     "--bind", "0.0.0.0:8001", \
     "--access-logfile", "-", \
     "--error-logfile", "-", \
     "ocr-server:app"]