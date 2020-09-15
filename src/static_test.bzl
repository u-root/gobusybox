"""This module contains a macro to test that a target is statically linked."""

def static_test(name, target, **kwargs):
    native.sh_test(
        name = name,
        timeout = "short",
        srcs = ["//src:static_test"],
        args = [
            "$(location %s)" % target,
        ],
        data = [
            target,
        ],
    )
