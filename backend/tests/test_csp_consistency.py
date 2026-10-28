"""
The Content-Security-Policy is declared in three places — the FastAPI security
middleware and the two bundled proxies (Caddyfile, nginx.unified.conf). A
browser enforces every policy it receives, so they must say the same thing.

Also checks how the image installs the bundled nginx config.
"""

import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]


def _proxy_policies(name: str) -> list[str]:
    text = (REPO_ROOT / name).read_text(encoding="utf-8")
    return re.findall(r'Content-Security-Policy "([^"]+)"', text)


def _backend_policy(client) -> str:
    policy = client.get("/health").headers["Content-Security-Policy"]
    # Tests run with DEBUG=true, which adds inline scripts for the Vite dev
    # server; production (what the proxies mirror) does not.
    return policy.replace("script-src 'self' 'unsafe-inline'", "script-src 'self'")


class TestCspConsistency:
    def test_proxies_mirror_the_backend_policy(self, client):
        backend = _backend_policy(client)
        caddy = _proxy_policies("Caddyfile")
        nginx = _proxy_policies("nginx.unified.conf")
        assert caddy and nginx
        for policy in caddy + nginx:
            assert policy == backend

    def test_policy_lists_no_font_cdn_and_no_ai_provider(self, client):
        policy = _backend_policy(client)
        assert "fonts.googleapis.com" not in policy
        assert "fonts.gstatic.com" not in policy
        assert "api.openai.com" not in policy
        assert "api.anthropic.com" not in policy

    def test_onedrive_picker_sources_are_allowed(self, client):
        policy = _backend_policy(client)
        directives = dict(d.strip().split(" ", 1) for d in policy.split(";") if " " in d.strip())
        assert "https://graph.microsoft.com" in directives["connect-src"].split()
        assert directives["form-action"].split() == [
            "'self'",
            "https://onedrive.live.com",
            "https://*.sharepoint.com",
        ]


class TestNginxConfigInImage:
    def test_site_config_is_root_owned_and_not_writable_by_appuser(self):
        """The root nginx master reads the site config. appuser runs the
        workers and the application, so it must not own — or be able to
        rewrite — what nginx serves and where it proxies to."""
        text = (REPO_ROOT / "Dockerfile").read_text(encoding="utf-8")
        copies = [
            line.strip()
            for line in text.splitlines()
            if line.startswith("COPY") and "nginx.unified.conf" in line
        ]
        assert copies == ["COPY nginx.unified.conf /etc/nginx/sites-enabled/default"]
        assert "chown root:root /etc/nginx/sites-enabled/default" in text
        assert "chmod 0644 /etc/nginx/sites-enabled/default" in text
