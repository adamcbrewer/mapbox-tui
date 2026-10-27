import copy
import json

from case_suite import FAILURE_MESSAGES, PUBLIC_FILE, definitions, load_suite, validate_case
from run import HERE, Stop, digest

REGISTERED = {"segment-distance-v1": ("segment-distance-v1", "import_distance_seed.patch"),
              "geometry-bounds-v1": ("geometry-bounds-v1", "import_bounds_seed.patch"),
              "coordinate-latitude-limits-v2": ("import-v1", "import_coordinate_seed.patch")}


def definition(identity=None):
    task = json.loads((HERE / "task.json").read_text())
    if identity is None or identity == task["id"]:
        return task
    if identity not in REGISTERED:
        raise Stop("invalid_input", "This task has no registered full checker")
    catalogue, seed_file = REGISTERED[identity]
    case = next(case for case in definitions(catalogue) if case["id"] == identity)
    messages = case.get("failure_messages", FAILURE_MESSAGES[case["partition"]])
    task.update(id=identity, expected_behaviour=case["description"], test=case["public_test"], cases=case["public_cases"],
                command=["go", "test", "./go", "-run", "^" + case["public_test"] + "$", "-count=1", "-json"],
                failure_message=messages["public"], allowed_new_files=[],
                fixture_provenance="Synthetic inline examples from the registered task fixtures; no personal activity files.",
                seed_sha256=digest((HERE / "fixtures" / seed_file).read_bytes()),
                regression={"test": case["private_test"], "cases": case["private_cases"],
                            "failure_message": messages["private"]})
    task["source_sha256"][PUBLIC_FILE] = digest((HERE / "fixtures" / case["public_fixture"]).read_bytes())
    return task


def materials(task):
    if task["id"] in REGISTERED:
        if task != definition(task["id"]):
            raise Stop("invalid_input", "The registered task definition changed")
        catalogue, seed_file = REGISTERED[task["id"]]
        case = next(case for case in definitions(catalogue) if case["id"] == task["id"])
        return {"task": (json.dumps(definition(task["id"]), indent=2) + "\n").encode(),
                "seed": (HERE / "fixtures" / seed_file).read_bytes(),
                "regression": (HERE / "fixtures" / case["private_fixture"]).read_bytes(),
                "public_files": {PUBLIC_FILE: (HERE / "fixtures" / case["public_fixture"]).read_bytes()}}
    if task["id"] != definition()["id"]:
        raise Stop("invalid_input", "This task has no registered full checker")
    return {"task": (HERE / "task.json").read_bytes(), "seed": (HERE / "fixtures/seed.patch").read_bytes(),
            "regression": (HERE / "fixtures/import_verifier_test.go.txt").read_bytes(), "public_files": {}}


def selected_task(suite, case_id=None):
    manifest = load_suite(suite)
    case = next((case for case in manifest["cases"] if case["id"] == case_id), None) if case_id else next(
        case for case in manifest["cases"] if case["partition"] == "development")
    if case is None:
        raise Stop("invalid_input", "The selected case isn't in this suite")
    validate_case(suite / "cases" / case["id"], case)
    task = definition(case["id"])
    inputs = materials(task)
    if (task["base_commit"] != case["base_commit"] or digest(inputs["seed"]) != case["seed_sha256"] or
            digest(inputs["regression"]) != case["private_files_sha256"]["verifier.go.txt"] or
            any(case["agent_files_sha256"].get(name) != expected for name, expected in task["source_sha256"].items()
                if name not in task["allowed_files"])):
        raise Stop("invalid_input", "The suite doesn't match the registered task inputs")
    return copy.deepcopy(task)
