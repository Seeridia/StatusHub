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


def deployment_state(rows, title, expected_sha=None, known_ids=()):
    if not isinstance(rows, list):
        raise ValueError('Unexpected deployment response')
    matches = [r for r in rows if r.get('title') == title or (
        expected_sha and r.get('deploymentId') not in known_ids
        and r.get('description') == 'Commit: ' + expected_sha
    )]
    if len(matches) > 1:
        raise ValueError('Ambiguous deployment result')
    return matches[0].get('status') if matches else None


def replace_environment_value(environment, key, value):
    if not isinstance(environment, str):
        raise ValueError('Unexpected Compose environment')
    if not value or '\n' in value or '\r' in value:
        raise ValueError('Invalid environment value')
    prefix = key + '='
    lines = environment.splitlines()
    matches = [index for index, line in enumerate(lines) if line.startswith(prefix)]
    if len(matches) != 1:
        raise ValueError('Expected exactly one ' + key + ' entry')
    lines[matches[0]] = prefix + value
    return '\n'.join(lines) + ('\n' if environment.endswith('\n') else '')


def main():
    base = https_origin(os.environ['DOKPLOY_URL'])
    public = https_origin(os.environ['STATUSHUB_PUBLIC_URL'])
    key = os.environ['DOKPLOY_API_KEY']
    compose = os.environ['DOKPLOY_COMPOSE_ID']
    image = os.environ['STATUSHUB_IMAGE']
    if not key or not compose or not image:
        raise ValueError('Dokploy API key, Compose ID, and image are required')
    opener = urllib.request.build_opener(NoRedirect)

    def request(url, payload=None, authenticated=True):
        headers = {'Content-Type': 'application/json', 'User-Agent': 'StatusHub-Deployment/1.0'}
        if authenticated:
            headers['x-api-key'] = key
        req = urllib.request.Request(url, data=json.dumps(payload).encode() if payload is not None else None, headers=headers)
        try:
            with opener.open(req, timeout=30) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            print('HTTP ' + str(error.code) + ' from ' + urllib.parse.urlsplit(url).path, flush=True)
            raise

    title = 'GitHub publish ' + os.environ['GITHUB_RUN_ID'] + '/' + os.environ['GITHUB_RUN_ATTEMPT']
    list_url = base + '/api/deployment.allByCompose?' + urllib.parse.urlencode({'composeId': compose})
    previous = request(list_url)
    if not isinstance(previous, list) or any(r.get('status') == 'running' for r in previous):
        raise ValueError('Another deployment is running or deployment history is unavailable')
    known_ids = {r.get('deploymentId') for r in previous}
    compose_config = request(base + '/api/compose.one?' + urllib.parse.urlencode({'composeId': compose}))
    if not isinstance(compose_config, dict):
        raise ValueError('Unexpected Compose response')
    updated_environment = replace_environment_value(compose_config.get('env'), 'STATUSHUB_IMAGE', image)
    request(base + '/api/compose.saveEnvironment', {
        'composeId': compose,
        'env': updated_environment,
        'createEnvFile': True,
    })
    # Do not retry POST: a lost response can still mean a deployment was queued.
    request(base + '/api/compose.deploy', {
        'composeId': compose, 'title': title,
        'description': 'Published commit ' + os.environ['GITHUB_SHA'],
    })
    print('Deployment queued; waiting for this run to finish.', flush=True)
    deadline = time.monotonic() + 900
    while time.monotonic() < deadline:
        rows = request(list_url)
        state = deployment_state(rows, title, os.environ['GITHUB_SHA'], known_ids)
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
