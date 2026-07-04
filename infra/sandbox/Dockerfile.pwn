# femto-sbx-pwn — the heavy tier for pwn / reverse / forensics: the binary toolchain
# (gdb + pwntools + RE/forensics tools). ~400MB-1GB; only pulled by nodes that run
# these categories. This is the "big for a reason" tier — binary tooling is heavy.
FROM python:3.12-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        binutils xxd file netcat-openbsd \
        gdb \
        gcc \
        libc6-dev \
        ltrace strace \
        binwalk \
        foremost \
        openssl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir pwntools ropgadget capstone unicorn \
    && find /usr/local/lib/python3.12 -name '__pycache__' -type d -prune -exec rm -rf {} + \
    && rm -rf /root/.cache
WORKDIR /task
CMD ["sleep", "3600"]
