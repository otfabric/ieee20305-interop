#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Tests of scripts/validate-documents.py, which the self-test runs on the
documents it keeps (REQUIREMENTS.md R3.3). Needs the jsonschema package, as
the script does; skipped without it."""

import contextlib
import copy
import importlib.util
import io
import json
import os
import pathlib
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HAVE_JSONSCHEMA = importlib.util.find_spec("jsonschema") is not None

CAPABILITIES = {
    "schemaVersion": "1.0", "adapter": "example", "adapterVersion": "dev", "contractVersion": "0.3",
    "upstream": {"name": "Example", "repository": "https://example.com/example", "version": "v1.0.0",
                 "revision": "0123456789abcdef0123456789abcdef01234567", "license": "MIT",
                 "language": "Go", "independentEngine": True},
    "tls": {"versions": ["1.2"], "cipherSuites": ["TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8"]},
    "versions": {"2018": {
        "client": {"supported": True, "operations": ["register", "discover"], "fixtures": ["baseline"]},
        "server": {"supported": False, "operations": []},
        "features": {"mTLS": True, "csip": False, "csipAus": False, "subscription": False},
    }},
}

RESULT = {
    "schemaVersion": "1.0", "adapter": "example", "version": "2018", "operation": "discover",
    "ok": True, "error": None, "request": {"method": "GET", "path": "/dcap"},
    "response": {"status": 200, "body": None}, "elapsedMs": 12,
}


@unittest.skipUnless(HAVE_JSONSCHEMA, "jsonschema is not installed")
class ValidateDocuments(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("validate_documents", os.path.join(HERE, "validate-documents.py"))
        cls.vd = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.vd)

    def run_on(self, **documents):
        """The exit code of validate-documents.py on a directory with these documents, and its output."""
        with tempfile.TemporaryDirectory() as tmp:
            for name, doc in documents.items():
                (pathlib.Path(tmp) / f"{name}.json").write_text(json.dumps(doc))
            out = io.StringIO()
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(out):
                code = self.vd.main([tmp])
            return code, out.getvalue()

    def test_valid_documents_pass(self):
        code, out = self.run_on(**{"capabilities-example": CAPABILITIES, "result-discover": RESULT})
        self.assertEqual(code, 0, out)
        self.assertIn("1 capability documents, 1 client results, 0 interop results, 0 errors", out)

    def test_a_capability_document_without_schema_version_fails(self):
        caps = copy.deepcopy(CAPABILITIES)
        del caps["schemaVersion"]
        code, out = self.run_on(**{"capabilities-example": caps, "result-discover": RESULT})
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL capabilities-example.json: (root): 'schemaVersion' is a required property", out)

    def test_a_result_document_without_schema_version_fails(self):
        result = copy.deepcopy(RESULT)
        del result["schemaVersion"]
        code, out = self.run_on(**{"capabilities-example": CAPABILITIES, "result-discover": result})
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL result-discover.json: (root): 'schemaVersion' is a required property", out)

    def test_a_field_of_the_wrong_type_fails(self):
        result = copy.deepcopy(RESULT)
        result["ok"] = "yes"
        code, out = self.run_on(**{"result-discover": result})
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL result-discover.json: ok:", out)

    def test_no_documents_fail(self):
        code, out = self.run_on()
        self.assertEqual(code, 1, out)
        self.assertIn("no documents found", out)

    def test_the_self_test_summary_is_not_a_document(self):
        code, out = self.run_on(**{"report-interop": {"summary": 1}, "result-discover": RESULT})
        self.assertEqual(code, 0, out)


if __name__ == "__main__":
    unittest.main()
