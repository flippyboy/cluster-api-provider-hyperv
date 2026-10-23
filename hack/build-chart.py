#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0

"""Write the Helm components template from infrastructure-components.yaml.

The chart installs the same objects clusterctl applies. Image, pull policy,
replica count, and leader election stay as values. Every other line is copied.
"""

import argparse
import re
from pathlib import Path


def split_docs(text: str) -> list[str]:
    docs = re.split(r"(?m)^---\s*$", text)
    return [doc.strip() for doc in docs if doc.strip()]


def transform(doc: str, image: str) -> str:
    lines = []
    for line in doc.splitlines():
        stripped = line.strip()
        indent = line[: len(line) - len(line.lstrip(" "))]
        if stripped.startswith("image: ") and stripped.split()[1].startswith(image + ":"):
            lines.append(
                indent
                + "image: {{ .Values.image.repository }}:{{ .Values.image.tag }}"
            )
            continue
        if stripped.startswith("imagePullPolicy:"):
            lines.append(indent + "imagePullPolicy: {{ .Values.image.pullPolicy }}")
            continue
        if line == "  replicas: 1":
            lines.append("  replicas: {{ .Values.replicaCount }}")
            continue
        if stripped == "- --leader-elect":
            lines.append("{{- if .Values.leaderElection }}")
            lines.append(line)
            lines.append("{{- end }}")
            continue
        lines.append(line)
    return "\n".join(lines)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--components", required=True)
    parser.add_argument("--chart", required=True)
    parser.add_argument("--image", required=True)
    args = parser.parse_args()

    docs = split_docs(Path(args.components).read_text())
    if not docs:
        raise SystemExit("infrastructure components yaml is empty")
    rendered = [transform(doc, args.image) for doc in docs]
    body = "\n---\n".join(rendered) + "\n"
    actions = body.count("{{")
    if actions != 6:
        raise SystemExit(f"expected 6 helm actions, found {actions}")
    if body.count(".Values.image.repository") != 1:
        raise SystemExit("manager image was not substituted once")

    dest = Path(args.chart) / "templates" / "components.yaml"
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(body)


if __name__ == "__main__":
    main()
