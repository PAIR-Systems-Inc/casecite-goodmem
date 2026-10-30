#!/usr/bin/env python3
"""Evaluate pinned Python provider policy, model routing and parameter negotiation."""
import ast
import copy
import hashlib
import ipaddress
import json
import logging
from pathlib import Path
import subprocess
import sys
from types import SimpleNamespace
from urllib.parse import urlparse

BASELINE = "6605cb3474cf0c2894080af216d5fed2a81fc997"
root = Path(sys.argv[1]).resolve()
sources = {}
settings = SimpleNamespace(hipaa_enforcement_enabled=True, approved_ai_providers=[])

def load(relative, names):
    raw = subprocess.check_output(["git", "show", f"{BASELINE}:{relative}"], cwd=root)
    sources[relative] = hashlib.sha256(raw).hexdigest()
    nodes = []
    for node in ast.parse(raw).body:
        name = getattr(node, "name", "")
        if isinstance(node, ast.Assign) and isinstance(node.targets[0], ast.Name):
            name = node.targets[0].id
        if name in names:
            nodes.append(node)
    env = dict(settings=settings, ipaddress=ipaddress, urlparse=urlparse, logger=logging.getLogger("oracle"))
    exec(compile(ast.fix_missing_locations(ast.Module(body=nodes, type_ignores=[])), relative, "exec"), env)
    return env

policy = load("backend/app/services/provider_policy.py", {
    "OPENAI_HOST", "SELF_HOSTED", "_SELF_HOSTED_ALIASES", "ProviderNotAllowedError",
    "_is_internal_host", "provider_for_base_url", "enforce_provider_allowed",
})
routing = load("backend/app/services/rag/generation.py", {"provider_of_model"})
clients = load("backend/app/services/llm_clients.py", {"_adapt_unsupported_params"})
urls = ["", "https://api.openai.com/v1", "https://API.OPENAI.COM/v1/", "http://localhost:11434/v1", "http://host.docker.internal:8080/v1", "http://ollama:11434/v1", "http://127.0.0.1/v1", "http://10.20.1.2/v1", "http://172.16.8.1/v1", "http://192.168.1.2/v1", "http://169.254.12.1/v1", "http://[::1]/v1", "http://[fd01::1]/v1", "https://api.groq.com/openai/v1", "https://openrouter.ai/api/v1", "https://api.openai.com.attacker.example/v1", "https://8.8.8.8/v1"]
fixtures = [dict(kind="endpoint", input=url, expected=policy["provider_for_base_url"](url)) for url in urls]
for model in ["", "gpt-5.5", "claude-sonnet-4-6", "ANTHROPIC/CLAUDE", "gemini-2.5-pro", "models/gemini-flash", "llama3.1", "custom-gpt", "gemini-claude"]:
    fixtures.append(dict(kind="model", input=model, expected=routing["provider_of_model"](model)))
for enabled in [False, True]:
    settings.hipaa_enforcement_enabled = enabled
    for approved in [[], ["openai"], ["self_hosted"], [" LOCAL "], ["self-hosted"], ["selfhosted"], ["anthropic", "google"], ["api.groq.com"]]:
        settings.approved_ai_providers = approved
        for name in ["openai", "anthropic", "google", "self_hosted", "api.groq.com", "openrouter.ai"]:
            try:
                policy["enforce_provider_allowed"](name)
                allowed = True
            except ValueError:
                allowed = False
            fixtures.append(dict(kind="policy", enabled=enabled, approved=approved, input=name, expected=allowed))
for message in ["Unsupported parameter: max_tokens", "Unsupported value: temperature", "Invalid model", "Invalid API key", "Maximum context length exceeded"]:
    before = dict(model="test", max_tokens=1024, temperature=0.1)
    after = copy.deepcopy(before)
    changed = clients["_adapt_unsupported_params"](after, message)
    after.update(after.pop("extra_body", {}))
    fixtures.append(dict(kind="adapt", input=before, message=message, expected=after, changed=changed))
dest = Path(__file__).resolve().parents[2]/"server/internal/app/testdata/provider_behavior.json"
dest.write_text(json.dumps(dict(baseline=BASELINE, sources=sources, fixtures=fixtures),indent=2)+"\n")
print(f"Wrote {len(fixtures)} provider behavior fixtures to {dest}")
