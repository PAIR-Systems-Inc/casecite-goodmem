"""Download/verify one pinned OCR snapshot inside the existing vLLM image."""
import hashlib
import json
import os
from pathlib import Path

REPO = "rednote-hilab/dots.ocr"
REVISION = "c0111ce6bc07803dbc267932ffef0ae3a51dc951"
snapshot = Path("/models/models--rednote-hilab--dots.ocr/snapshots") / REVISION

if os.environ.get("OCR_CACHE_READ_ONLY") != "true":
    from huggingface_hub import snapshot_download

    snapshot_download(
        REPO, revision=REVISION, cache_dir="/models",
        allow_patterns=["*.json", "*.safetensors", "*.py", "*.txt", "*.tiktoken"],
        max_workers=2,
    )

# A partially downloaded directory is not a valid model. Check every indexed
# shard and its content hash (Hugging Face names LFS blobs by SHA-256).
for name in ["config.json", "tokenizer.json", "preprocessor_config.json", "model.safetensors.index.json"]:
    if not (snapshot / name).is_file():
        raise SystemExit(f"Missing {name} in pinned OCR snapshot {REVISION}")
index = json.loads((snapshot / "model.safetensors.index.json").read_text())
shards = sorted(set(index["weight_map"].values()))
if not shards:
    raise SystemExit("OCR snapshot has no weights")
for name in shards:
    path = (snapshot / name).resolve(strict=True)
    if len(path.name) != 64 or any(c not in "0123456789abcdef" for c in path.name):
        raise SystemExit("Expected an intact Hugging Face cache with SHA-256 weight blobs")
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(8 * 1024 * 1024), b""):
            digest.update(chunk)
    if digest.hexdigest() != path.name:
        raise SystemExit(f"OCR weight checksum mismatch: {name}")
    print(f"Verified {name}", flush=True)
print(f"OCR snapshot {REVISION} is ready", flush=True)
