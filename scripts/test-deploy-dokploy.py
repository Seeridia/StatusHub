import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location('deploy', Path(__file__).with_name('deploy-dokploy.py'))
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)

class DeploymentTests(unittest.TestCase):
    def test_reject_unsafe_origins(self):
        for url in ('http://example.com', 'https://key@example.com', 'https://example.com/path', 'https://example.com/?token=x'):
            with self.assertRaises(ValueError): deploy.https_origin(url)
        self.assertEqual(deploy.https_origin('https://example.com/'), 'https://example.com')

    def test_only_current_deployment_can_pass(self):
        rows = [{'title': 'old', 'status': 'done'}, {'title': 'current', 'status': 'running'}]
        self.assertEqual(deploy.deployment_state(rows, 'current'), 'running')
        self.assertIsNone(deploy.deployment_state(rows, 'missing'))
        with self.assertRaises(ValueError): deploy.deployment_state(rows + [rows[1]], 'current')

    def test_dokploy_commit_title_rewrite(self):
        rows = [{'deploymentId': 'old', 'description': 'Commit: abc', 'status': 'done'},
                {'deploymentId': 'new', 'title': 'Git commit title', 'description': 'Commit: abc', 'status': 'running'}]
        self.assertEqual(deploy.deployment_state(rows, 'workflow title', 'abc', {'old'}), 'running')
        self.assertIsNone(deploy.deployment_state(rows, 'workflow title', 'other', {'old'}))

    def test_credentials_not_redirected(self):
        self.assertIsNone(deploy.NoRedirect().redirect_request(None, None, 302, '', {}, 'https://other.example'))

if __name__ == '__main__': unittest.main()
