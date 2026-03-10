#!/usr/bin/env python3
"""Extract speaker embedding vectors using NeMo TitaNet."""

from __future__ import annotations

import argparse
import hashlib
import json
from datetime import datetime, timezone

import torch
from nemo.collections.asr.models import EncDecSpeakerLabelModel


def sha256_file(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser(description="Extract TitaNet embedding")
    parser.add_argument("--input", required=True, help="Path to source voice clip")
    parser.add_argument("--output", required=True, help="Output JSON path")
    parser.add_argument("--model", default="titanet_large", help="NeMo speaker model name")
    args = parser.parse_args()

    model = EncDecSpeakerLabelModel.from_pretrained(model_name=args.model)
    model.eval()
    if torch.cuda.is_available():
        model = model.to("cuda")

    with torch.no_grad():
        embedding = model.get_embedding(args.input)

    vector = embedding.detach().cpu().reshape(-1).tolist()
    payload = {
        "version": 1,
        "model": args.model,
        "dimension": len(vector),
        "vector": vector,
        "source_sha256": sha256_file(args.input),
        "created_at": datetime.now(timezone.utc).isoformat(),
    }

    with open(args.output, "w", encoding="utf-8") as handle:
        json.dump(payload, handle, indent=2)


if __name__ == "__main__":
    main()
