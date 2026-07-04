import json
import os
import tempfile
import unittest

import maya_stall


class ScenarioResultTests(unittest.TestCase):
    def test_write_result_reads_path_from_environment(self):
        with tempfile.TemporaryDirectory() as tmp:
            result_path = os.path.join(tmp, "outputs", "result.json")
            env = {"MAYA_STALL_SCENARIO_RESULT": result_path}

            written = maya_stall.write_result(
                status="passed",
                summary="mesh exported",
                assertions=[{"name": "plugin loaded", "passed": True}],
                measurements={"solveMs": 12.5},
                outputs={"report": "outputs/report.json"},
                env=env,
            )

            self.assertEqual(written, result_path)
            with open(result_path, encoding="utf-8") as handle:
                result = json.load(handle)
            self.assertEqual(
                result,
                {
                    "status": "passed",
                    "summary": "mesh exported",
                    "assertions": [{"name": "plugin loaded", "passed": True}],
                    "measurements": {"solveMs": 12.5},
                    "outputs": {"report": "outputs/report.json"},
                },
            )

    def test_write_result_requires_environment_path(self):
        with self.assertRaisesRegex(
            maya_stall.ResultPathError,
            "MAYA_STALL_SCENARIO_RESULT is not set",
        ):
            maya_stall.write_result(env={})

    def test_write_result_requires_non_empty_status(self):
        with tempfile.TemporaryDirectory() as tmp:
            env = {"MAYA_STALL_SCENARIO_RESULT": os.path.join(tmp, "result.json")}

            with self.assertRaisesRegex(ValueError, "status must not be empty"):
                maya_stall.write_result(status="", env=env)

if __name__ == "__main__":
    unittest.main()
