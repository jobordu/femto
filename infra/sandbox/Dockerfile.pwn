# femto-sbx-pwn — the heavy tier for pwn / reverse / forensics: the binary toolchain
# (gdb + pwntools + RE/forensics tools). ~400MB-1GB; only pulled by nodes that run
# these categories. This is the "big for a reason" tier — binary tooling is heavy.
FROM python:3.12-slim
RUN DEBIAN_FRONTEND=noninteractive apt-get update && apt-get install -y --no-install-recommends \
        binutils xxd file netcat-openbsd \
        gdb \
        gcc \
        libc6-dev \
        ltrace strace \
        binwalk \
        foremost \
        steghide libimage-exiftool-perl sleuthkit tshark pngcheck john \
        unzip bsdextrautils tcpdump \
        openssl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# Forensics tier was missing the tools NYU-CTF forensics actually needs (stego/memory/disk/pcap),
# so every forensics run failed for lack of a tool, not lack of model skill. Added: steghide+exiftool
# (stego), sleuthkit (disk), tshark (pcap), pngcheck, john, volatility3 (memory). Needs a sandbox
# rebuild (infra/sandbox/build.sh) + a re-sweep to convert into signal.
RUN pip install --no-cache-dir pwntools ropgadget capstone unicorn volatility3 \
    && find /usr/local/lib/python3.12 -name '__pycache__' -type d -prune -exec rm -rf {} + \
    && rm -rf /root/.cache
WORKDIR /task
CMD ["sleep", "3600"]
