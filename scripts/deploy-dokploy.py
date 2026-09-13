#!/usr/bin/env python3
"""Trigger one deployment after publish; never print credentials or API bodies."""
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # Do not forward the API credential to another origin.


def https_origin(value):
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme != 'https' or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in ('', '/'):
        raise ValueError('Expected an HTTPS origin without path, credentials or query')
    return value.rstrip('/')


def deployment_state(rows, title):
    if not isinstance(rows, list):
        raise ValueError('Unexpected deployment response')
    matches = [r for r in rows if r.get('title') == title]
    if len(matches) > 1:
        raise ValueError('Ambiguous deployment result')
    return matches[0].get('status') if matches else None


def main():
    base = https_origin(os.environ['DOKPLOY_URL'])
    public = https_origin(os.environ['STATUSMON_PUBLIC_URL'])
    key = os.environ['DOKPLOY_API_KEY']
    compose = os.environ['DOKPLOY_COMPOSE_ID']
    if not key or not compose:
        raise ValueError('Dokploy API key and Compose ID are required')
    opener = urllib.request.build_opener(NoRedirect)

    def request(url, payload=None, authenticated=True):
        headers = {'Content-Type': 'application/json'}
        if authenticated:
            headers['x-api-key'] = key
        req = urllib.request.Request(url, data=json.dumps(payload).encode() if payload is not None else None, headers=headers)
        with opener.open(req, timeout=30) as response:
            return json.load(response)

    title = 'GitHub publish ' + os.environ['GITHUB_RUN_ID'] + '/' + os.environ['GITHUB_RUN_ATTEMPT']
    # Do not retry POST: a lost response can still mean a deployment was queued.
    request(base + '/api/compose.deploy', {
        'composeId': compose, 'title': title,
        'description': 'Published commit ' + os.environ['GITHUB_SHA'],
    })
    print('Deployment queued; waiting for this run to finish.', flush=True)
    deadline = time.monotonic() + 900
    while time.monotonic() < deadline:
        rows = request(base + '/api/deployment.allByCompose?' + urllib.parse.urlencode({'composeId': compose}))
        state = deployment_state(rows, title)
        if state == 'done':
            break
        if state in ('error', 'cancelled'):
            raise ValueError('Dokploy deployment failed or was cancelled; inspect Dokploy logs')
        time.sleep(10)
    else:
        raise TimeoutError('Timed out waiting for Dokploy deployment')
    for attempt in range(12):
        try:
            for path in ('/healthz', '/readyz'):
                request(public + path, authenticated=False)
            print('Dokploy deployment completed; health and readiness checks passed.')
            return
        except (urllib.error.URLError, ValueError):
            if attempt == 11:
                raise ValueError('Deployment completed but public health checks failed') from None
            time.sleep(5)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # API errors may contain secret-bearing URLs or response bodies.
        print('Deployment verification failed (' + type(error).__name__ + '). Check configuration and Dokploy deployment logs.', file=sys.stderr)
        sys.exit(1)
