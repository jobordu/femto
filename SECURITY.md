# Security Policy

## Intended use

femto is a **security research and CTF** tool. It runs LLM-generated commands in a
sandbox and is meant for:

- Capture-the-flag competitions and practice challenges you are authorized to solve.
- Security research and evaluation on systems you own or have **explicit written
  permission** to test.
- Building and studying agent harnesses.

Do **not** use femto to access, attack, or test systems without authorization. You are
responsible for complying with all applicable laws and the terms of any target system.

## Sandbox expectations

femto executes model-generated shell/python. The `docker` executor runs each task in a
disposable, **network-off**, resource-capped container — use it for anything
adversarial. The `local` executor has **no isolation** and is for trusted toy tasks and
tests only. Never point `local` at untrusted challenges.

## Reporting a vulnerability

If you find a security issue in femto itself (e.g. a sandbox escape, or a way for a
malicious model response to break isolation), please report it privately via GitHub
Security Advisories ("Report a vulnerability" on the repository's Security tab) rather
than opening a public issue. We aim to acknowledge reports within a few days.
