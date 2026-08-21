"""An aspect that runs go-mutesting over rules_go targets.

Apply it to any go_library, go_binary or go_test target:

    bazel build //... \\
      --aspects=@rules_go_mutesting//mutesting:aspect.bzl%go_mutesting_aspect \\
      --output_groups=mutesting_report

Mutation testing only produces a score where there are tests to kill mutants
with, so go_test targets are the useful ones; the library and binary kinds are
supported so that a repository-wide sweep does not need target filtering.
"""

load("@bazel_skylib//lib:paths.bzl", "paths")
load("@bazel_skylib//rules:common_settings.bzl", "BuildSettingInfo")
load("@rules_go//go:def.bzl", "GoArchive", "GoInfo")
load(
    "//mutesting:settings.bzl",
    "BOOL_SETTINGS",
    "INT_SETTINGS",
    "LABEL_SETTINGS",
    "STRING_LIST_SETTINGS",
    "STRING_SETTINGS",
    "attr_name",
    "setting_label",
)

GO_TOOLCHAIN = "@rules_go//go:toolchain"

_SUPPORTED_KINDS = ["go_library", "go_binary", "go_test"]

# Optional reports, keyed by the setting that turns them on. The tool writes
# each to a fixed name in its working directory; the runner moves them onto
# these declared outputs.
_OPTIONAL_REPORTS = {
    "html_output": ("html", "mutesting.html"),
    "logger_summary_json": ("summary", "mutesting.summary.json"),
    "logger_agentic_json": ("agentic", "mutesting.agentic.json"),
    "logger_gitlab": ("gitlab", "mutesting.gitlab.json"),
}

def _settings_json(ctx):
    """Serializes every build setting into the runner's settings contract."""
    bools = {}
    for name, _default, _doc in BOOL_SETTINGS:
        bools[name] = getattr(ctx.attr, attr_name(name))[BuildSettingInfo].value

    strings = {}
    for name, _default, _doc in STRING_SETTINGS:
        strings[name] = getattr(ctx.attr, attr_name(name))[BuildSettingInfo].value

    ints = {}
    for name, _default, _doc in INT_SETTINGS:
        ints[name] = getattr(ctx.attr, attr_name(name))[BuildSettingInfo].value

    lists = {}
    for name, _doc in STRING_LIST_SETTINGS:
        lists[name] = getattr(ctx.attr, attr_name(name))[BuildSettingInfo].value

    files = {}
    inputs = []
    for name, _sentinel, _doc in LABEL_SETTINGS:
        f = _single_file(getattr(ctx.attr, attr_name(name)))
        if f == None:
            files[name] = ""
            continue
        files[name] = f.path
        inputs.append(f)

    return struct(bools = bools, strings = strings, ints = ints, lists = lists, files = files), inputs

def _single_file(target):
    files = target[DefaultInfo].files.to_list()
    if not files:
        return None
    return files[0]

def _source_entry(f, name = None):
    """Describes one file of the package under test."""
    return struct(
        path = f.path,
        name = name or f.basename,
        # Only files that exist in the source tree can be matched against a
        # git diff; generated ones deliberately carry no workspace path.
        workspace = f.short_path if f.is_source else "",
    )

def _dep_source_entry(f, name = None):
    """Describes one file of a dependency package.

    Dependency sources deliberately carry no workspace path. They are staged
    only so the package under test compiles, and the file selection options
    match on workspace paths: without this, a target would mutate its vendored
    copy of a dependency's source, duplicating mutants that belong to the
    target that actually owns that file.
    """
    return struct(path = f.path, name = name or f.basename)

# Extensions the staged module needs. Assembly is compiled even with cgo
# disabled, and a package that implements a function in assembly declares the
# Go side of it with no body, so dropping the .s file turns into "missing
# function body" at compile time. Headers come along for the assembly to
# include. C sources deliberately do not: the go command rejects them outright
# when cgo is off.
_STAGED_EXTENSIONS = ["go", "s", "S", "h"]

def _staged_files(files):
    return [f for f in files if f.extension in _STAGED_EXTENSIONS]

def _package_dirs(files):
    """The distinct directories a package's files sit in."""
    return {paths.dirname(f.path): None for f in files}.keys()

def _embed_name(f, dirs):
    """Names an embedded file relative to its package directory.

    A //go:embed pattern can reach into subdirectories, so staging embedded
    files under their basename would flatten the tree and the pattern would
    stop matching. Bazel files carry no package directory, so it is recovered
    from the directories the package's Go sources sit in; an embedded file
    generated into a different root falls back to its basename.
    """
    best = ""
    for d in dirs:
        if f.path.startswith(d + "/") and len(d) > len(best):
            best = d
    if not best:
        return f.basename
    return f.path[len(best) + 1:]

def _runfile_entry(f, workspace):
    """Describes one runfile by its path inside the runfiles tree.

    Bazel spells a file from an external repository as ../<repo>/<path>
    relative to the main workspace directory, which is one level below the
    runfiles root; everything else is relative to the main workspace itself.
    """
    short_path = f.short_path
    if short_path.startswith("../"):
        name = short_path[len("../"):]
    else:
        name = workspace + "/" + short_path
    return struct(path = f.path, name = name)

def _package_sources(target, ctx, kind):
    """Returns (importpath, srcs, embedsrcs, dep_archives) for the target."""
    if kind == "go_test":
        # A go_test carries the test sources; the package under test comes
        # from the libraries it embeds.
        srcs = list(_staged_files(ctx.rule.files.srcs))
        embedsrcs = []
        importpath = getattr(ctx.rule.attr, "importpath", "")
        dep_archives = []
        for e in getattr(ctx.rule.attr, "embed", []):
            if GoInfo not in e:
                continue
            info = e[GoInfo]
            srcs.extend(_staged_files(info.srcs))
            embedsrcs.extend(info.embedsrcs)
            if not importpath:
                importpath = info.importpath
            if GoArchive in e:
                dep_archives.append(e[GoArchive])
        for d in getattr(ctx.rule.attr, "deps", []):
            if GoArchive in d:
                dep_archives.append(d[GoArchive])
        return importpath, srcs, embedsrcs, dep_archives

    info = target[GoInfo]
    dep_archives = []
    for d in getattr(ctx.rule.attr, "deps", []) + getattr(ctx.rule.attr, "embed", []):
        if GoArchive in d:
            dep_archives.append(d[GoArchive])
    return (
        info.importpath,
        _staged_files(info.srcs),
        list(info.embedsrcs),
        dep_archives,
    )

def _x_defs(target, ctx, importpath):
    """Collects the target's -X linker values, keyed by qualified symbol.

    A test that reads a variable stamped in by x_defs -- the path of a fixture
    it is given through data, most often -- sees an empty string without them,
    so the staged module has to stamp the same values. Values are expanded the
    way rules_go expands them, which is what turns $(rlocationpath ...) into a
    path that resolves inside the staged runfiles tree.
    """
    x_defs = {}
    for e in getattr(ctx.rule.attr, "embed", []):
        if GoInfo in e:
            x_defs.update(e[GoInfo].x_defs)
    if GoInfo in target:
        x_defs.update(target[GoInfo].x_defs)
    for k, v in getattr(ctx.rule.attr, "x_defs", {}).items():
        if "$" in v:
            v = ctx.expand_location(v, getattr(ctx.rule.attr, "data", []))
        if "." not in k:
            k = importpath + "." + k
        x_defs[k] = v
    return x_defs

def _uses_cgo(target, ctx, kind):
    """Reports whether the package under test itself needs cgo.

    Only the package being mutated is checked. A dependency marked cgo = True
    is usually still buildable with cgo off -- the C path is one build-tagged
    implementation among several, as it is for golang.org/x/sys/unix -- and
    skipping on that basis would drop most of a real repository. A dependency
    that genuinely cannot build without a C toolchain is caught instead by the
    compile check the runner makes before it mutates anything.
    """
    if getattr(ctx.rule.attr, "cgo", False):
        return True
    if kind != "go_test" and GoInfo in target and target[GoInfo].cgo:
        return True
    for e in getattr(ctx.rule.attr, "embed", []):
        if GoInfo in e and e[GoInfo].cgo:
            return True
    return False

def _impl(target, ctx):
    kind = ctx.rule.kind
    if kind not in _SUPPORTED_KINDS:
        return []
    if kind != "go_test" and GoInfo not in target:
        return []

    importpath, srcs, embedsrcs, dep_archives = _package_sources(target, ctx, kind)
    if not srcs:
        return []
    if not importpath:
        # Without an import path there is no module path to synthesize.
        importpath = "mutesting.invalid/" + ctx.label.package.replace("/", "_")

    dep_datas = depset(transitive = [a.transitive for a in dep_archives]).to_list()

    deps = []
    dep_inputs = []
    for data in dep_datas:
        ip = data.importmap or data.importpath
        if not ip or ip == importpath or ip == importpath + "_test":
            # The package under test is staged from its own sources.
            continue
        dep_srcs = _staged_files(list(data.srcs))
        if not dep_srcs:
            continue

        # Files the dependency reaches through //go:embed. Staging its Go
        # sources without them leaves the embed pattern matching nothing, and
        # the staged module fails to build -- which mutation testing scores as
        # a killed mutant, so every mutant would look killed.
        dep_embedsrcs = list(getattr(data, "_embedsrcs", []))
        dep_dirs = _package_dirs(dep_srcs)
        deps.append(struct(
            importpath = ip,
            srcs = [_dep_source_entry(f) for f in dep_srcs],
            embedsrcs = [
                _dep_source_entry(f, _embed_name(f, dep_dirs))
                for f in dep_embedsrcs
            ],
        ))
        dep_inputs.extend(dep_srcs + dep_embedsrcs)

    # Data dependencies. A test that reads one looks it up through the
    # runfiles libraries, so the tree has to be rebuilt around the staged
    # module or every mutant dies on a missing file.
    runfiles = target[DefaultInfo].default_runfiles
    runfiles_inputs = runfiles.files.to_list()
    repo_mapping = getattr(runfiles, "repo_mapping_manifest", None)
    if repo_mapping:
        runfiles_inputs.append(repo_mapping)

    go = ctx.toolchains[GO_TOOLCHAIN]
    sdk = go.sdk

    settings, setting_inputs = _settings_json(ctx)

    name = ctx.label.name
    report = ctx.actions.declare_file(name + ".mutesting.report.json")
    log = ctx.actions.declare_file(name + ".mutesting.log")
    outs = [report, log]

    args = ctx.actions.args()
    args.add("--report-out", report)
    args.add("--log-out", log)

    for setting, (flag, suffix) in _OPTIONAL_REPORTS.items():
        if settings.bools[setting]:
            f = ctx.actions.declare_file("{}.{}".format(name, suffix))
            outs.append(f)
            args.add("--{}-out".format(flag), f)

    manifest = ctx.actions.declare_file(name + ".mutesting-manifest.json")
    ctx.actions.write(manifest, json.encode(struct(
        importpath = importpath,
        go_version = sdk.version,
        label = str(ctx.label),
        srcs = [_source_entry(f) for f in srcs],
        embedsrcs = [
            _source_entry(f, _embed_name(f, _package_dirs(srcs)))
            for f in embedsrcs
        ],
        deps = deps,
        goroot = paths.dirname(sdk.root_file.path),
        has_cgo = _uses_cgo(target, ctx, kind),
        runfiles = [
            _runfile_entry(f, ctx.workspace_name)
            for f in runfiles.files.to_list()
        ],
        repo_mapping = repo_mapping.path if repo_mapping else "",
        x_defs = _x_defs(target, ctx, importpath),
    )))

    settings_file = ctx.actions.declare_file(name + ".mutesting-settings.json")
    ctx.actions.write(settings_file, json.encode(settings))

    args.add("--manifest", manifest)
    args.add("--settings", settings_file)
    args.add("--tool", ctx.executable._tool)

    execution_requirements = {}
    workers = settings.ints["workers"]
    if workers > 0:
        # Mutation runs are many short test binaries; tell Bazel how much of
        # the machine this action intends to use.
        execution_requirements["cpu:" + str(workers)] = ""

    ctx.actions.run(
        executable = ctx.executable._runner,
        arguments = [args],
        inputs = depset(
            srcs + embedsrcs + dep_inputs + runfiles_inputs + setting_inputs + [manifest, settings_file],
            transitive = [sdk.srcs, sdk.libs, sdk.headers, sdk.tools, depset([sdk.go, sdk.root_file])],
        ),
        tools = [ctx.executable._tool],
        outputs = outs,
        mnemonic = "GoMutesting",
        progress_message = "Mutation testing %{label}",
        execution_requirements = execution_requirements,
        toolchain = GO_TOOLCHAIN,
    )

    return [OutputGroupInfo(mutesting_report = depset(outs))]

def _aspect_attrs():
    attrs = {
        "_runner": attr.label(
            default = Label("//mutesting/internal/runner"),
            executable = True,
            cfg = "exec",
        ),
        "_tool": attr.label(
            default = Label("//mutesting:tool"),
            executable = True,
            cfg = "exec",
        ),
    }
    for name, _default, _doc in BOOL_SETTINGS:
        attrs[attr_name(name)] = attr.label(default = setting_label(name))
    for name, _default, _doc in STRING_SETTINGS:
        attrs[attr_name(name)] = attr.label(default = setting_label(name))
    for name, _default, _doc in INT_SETTINGS:
        attrs[attr_name(name)] = attr.label(default = setting_label(name))
    for name, _doc in STRING_LIST_SETTINGS:
        attrs[attr_name(name)] = attr.label(default = setting_label(name))
    for name, _sentinel, _doc in LABEL_SETTINGS:
        attrs[attr_name(name)] = attr.label(default = setting_label(name), allow_files = True)
    return attrs

go_mutesting_aspect = aspect(
    implementation = _impl,
    # Dependency sources arrive through the GoArchive providers, so there is no
    # need to re-run the aspect down the dependency graph.
    attr_aspects = [],
    attrs = _aspect_attrs(),
    toolchains = [GO_TOOLCHAIN],
)
