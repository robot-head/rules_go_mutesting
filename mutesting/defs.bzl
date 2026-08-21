"""Public rules for mutation testing Go targets."""

load("//mutesting:aspect.bzl", "go_mutesting_aspect")

def _go_mutesting_test_impl(ctx):
    reports = ctx.attr.target[OutputGroupInfo].mutesting_report.to_list()
    report = None
    for f in reports:
        if f.basename.endswith(".mutesting.report.json"):
            report = f
            break
    if report == None:
        fail("the mutation aspect produced no report for {}".format(ctx.attr.target.label))

    checker = ctx.executable._reportcheck
    script = ctx.actions.declare_file(ctx.label.name + ".sh")
    ctx.actions.write(
        output = script,
        content = """#!/bin/sh
exec "{checker}" -report "{report}" -min-msi "{min_msi}" -label "{label}"
""".format(
            checker = checker.short_path,
            report = report.short_path,
            min_msi = ctx.attr.min_msi,
            label = str(ctx.attr.target.label),
        ),
        is_executable = True,
    )

    return [DefaultInfo(
        executable = script,
        runfiles = ctx.runfiles(files = reports + [checker]),
    )]

go_mutesting_test = rule(
    implementation = _go_mutesting_test_impl,
    doc = """Runs mutation testing on a Go target and reports the result as a test.

The mutation run itself happens in the aspect's cached action, so repeated
`bazel test` invocations only re-run it when the sources or options change.

    go_mutesting_test(
        name = "adder_mutation_test",
        target = "//examples/adder:adder_test",
        min_msi = "50",
    )
""",
    attrs = {
        "target": attr.label(
            doc = "The go_test (preferred), go_library or go_binary to mutate.",
            aspects = [go_mutesting_aspect],
            mandatory = True,
        ),
        "min_msi": attr.string(
            doc = "Minimum mutation score as a percentage. Empty means report only.",
            default = "-1",
        ),
        "_reportcheck": attr.label(
            default = Label("//mutesting/internal/reportcheck"),
            executable = True,
            cfg = "exec",
        ),
    },
    test = True,
)
