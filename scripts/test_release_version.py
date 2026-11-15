import unittest
from release_version import choose

class ReleaseVersionTests(unittest.TestCase):
    def test_initial(self):
        self.assertEqual(choose('0.1.0', [], 'new'), 'v0.1.0-1')
    def test_next(self):
        self.assertEqual(choose('0.1.0', [('v0.1.0-2', 'old'), ('v0.1.0-10', 'older')], 'new'), 'v0.1.0-11')
    def test_retry_same_source(self):
        self.assertEqual(choose('0.1.0', [('v0.1.0-2', 'same')], 'same'), 'v0.1.0-2')
    def test_new_upstream(self):
        self.assertEqual(choose('0.2.0', [('v0.1.0-8', 'old')], 'new'), 'v0.2.0-1')
    def test_ignores_nonrelease_tags(self):
        self.assertEqual(choose('0.1.0', [('v0.1.0-01', 'old'), ('v0.1.0', 'old')], 'new'), 'v0.1.0-1')
