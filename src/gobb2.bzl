"""This module contains macros for building u-root busybox-style Go binaries.

Example usage to create a busybox binary:

  go_busybox(
    name = "bb",
    commands = [
      "//foo/bar/cmd/ls",
      "//foo/bar/cmd/ip",
    ],
  )
"""

load("@io_bazel_rules_go//go:def.bzl", "go_binary", "go_context", "go_library", "go_rule")
load("@io_bazel_rules_go//go/private:rules/transition.bzl", "go_transition_rule")
load("@io_bazel_rules_go//go/private:providers.bzl", "GoArchive", "GoLibrary", "GoSource")
load(
    "@io_bazel_rules_go//go/platform:list.bzl",
    "GOARCH",
    "GOOS",
)

GoDepInfo = provider("targets")
CommandNamesInfo = provider("cmd_names")

def _go_dep_aspect(target, ctx):
    if ctx.rule.kind == "go_binary":
        deps = []
        for embed in ctx.rule.attr.embed:
            if GoDepInfo in embed:
                deps += embed[GoDepInfo].targets
        return [GoDepInfo(targets = deps)]

    if ctx.rule.kind == "go_library":
        targets = [target]
        for dep in ctx.rule.attr.deps:
            if GoDepInfo in dep:
                targets += dep[GoDepInfo].targets

        return [GoDepInfo(targets = targets)]

# An aspect that collects all recursive Target deps.
go_dep_aspect = aspect(
    implementation = _go_dep_aspect,
    attr_aspects = ["deps", "embed"],
    provides = [GoDepInfo],
)

def _go_busybox_library(ctx):
    """Rewrite one Go command to be a library.

    It will take a go_binary's source files and rewrite them to be compatible
    with u-root's busybox mode as a library.

    Args:
      ctx: rule context

    Returns:
      GoLibrary, GoSource, GoArchive like a normal go_library
    """
    args = ctx.actions.args()
    args.add("--name", ctx.attr.cmd[GoLibrary].name)
    args.add("--bb_import_path", "github.com/u-root/gobusybox/src/pkg/bb/bbmain")

    go = go_context(ctx)
    for archive in go.stdlib.libs:
        args.add("--stdlib_archive", archive.path)

    output_dir = None
    outputs = []
    inputSrcs = []
    depInputs = []
    transitiveDepTargets = []
    importpath = None

    importpath = ctx.attr.cmd[GoLibrary].importpath
    for deparchive in ctx.attr.cmd[GoArchive].direct:
        args.add("--mapped_archive", "%s:%s" % (deparchive.data.importpath, deparchive.data.file.path))
        depInputs.append(deparchive.data.file)

    # Transitive dependencies of the direct dependency.
    for tdep in ctx.attr.cmd[GoArchive].transitive.to_list():
        args.add("--mapped_archive", "%s:%s" % (tdep.importpath, tdep.file.path))
        depInputs.append(tdep.file)

    transitiveDepTargets = ctx.attr.cmd[GoDepInfo].targets

    for f in ctx.attr.cmd[GoSource].srcs:
        args.add("--source", f.path)
        inputSrcs.append(f)

        # This relies on f.basename being relative to output_dir, which
        # they should be since they're relative to gen.. It's a
        # bit of a hack.
        outf = go.actions.declare_file("%s/gen2/%s" % (f.dirname, f.basename))
        outputs.append(outf)
        if not output_dir:
            output_dir = outf.dirname

    args.add("--dest_dir", output_dir)

    # go.sdk.goarch/goos seem to have host values? Why do those differ form the
    # go.env values?
    args.add("--goarch", go.env["GOARCH"])
    args.add("--goos", go.env["GOOS"])

    # Run the rewritepkg binary.
    ctx.actions.run(
        inputs = depset(inputSrcs, transitive = [depset(depInputs), depset(go.stdlib.libs)]),
        outputs = outputs,
        arguments = [args],
        executable = ctx.executable._rewrite_ast,
    )

    library = go.new_library(
        name = ctx.attr.name,
        go = go,
        importpath = "%s_bb" % importpath,
        srcs = outputs,
    )
    attr = struct(
        deps = transitiveDepTargets + ctx.attr._new_deps,
        cgo = False,
    )
    source = go.library_to_source(go, attr, library, ctx.coverage_instrumented())
    archive = go.archive(go, source)
    return [
        library,
        source,
        archive,
    ]

go_busybox_library = go_rule(
    implementation = _go_busybox_library,
    attrs = {
        "cmd": attr.label(
            mandatory = True,
            providers = [GoDepInfo, GoSource, GoLibrary, GoArchive],
            allow_rules = ["go_binary"],
            aspects = [go_dep_aspect],
        ),
        "_stdlib": attr.label(
            default = Label("@io_bazel_rules_go//:stdlib"),
        ),
        "_rewrite_ast": attr.label(
            executable = True,
            cfg = "host",
            allow_files = True,
            default = Label("//cmd/rewritepkg"),
        ),
        "_new_deps": attr.label_list(
            default = ["//pkg/bb/bbmain"],
        ),
        "_go_context_data": attr.label(
            default = "@io_bazel_rules_go//:go_context_data",
        ),
    },
    toolchains = ["@io_bazel_rules_go//go:toolchain"],
)

def _go_busybox_impl(ctx):
    """_go_busybox_impl creates + compiles the main.go dispatcher.

    It takes a set of go_binary dependencies to be compiled into one busybox
    binary and generates the appropriate main() package.

    Args:
        ctx: rule context.

    Returns:
        The set of generated Go source files that contain a main() function.
    """
    output_dir = None

    args = ctx.actions.args()
    args.add("--template_pkg", "%s/main" % ctx.attr._template.label.package)

    outputs = []
    inputs = []
    for f in ctx.attr._template[GoArchive].source.srcs:
        args.add("--package_file", f.path)
        inputs.append(f)

        outf = ctx.actions.declare_file("%s_bbgen/%s" % (ctx.attr.name, f.basename))
        outputs.append(outf)
        if not output_dir:
            output_dir = outf.dirname

    args.add("--dest_dir", output_dir)

    # Stuff to import.
    for cmd in ctx.attr.cmds:
        args.add("--command", cmd[GoLibrary].importpath)

    # Run the make_main binary.
    ctx.actions.run(
        inputs = inputs,
        outputs = outputs,
        arguments = [args],
        executable = ctx.executable._make_main,
    )

    go = go_context(ctx)
    library = go.new_library(
        go = go,
        importable = True,
        srcs = outputs,
        is_main = True,
    )
    attr = struct(
        deps = ctx.attr.cmds + ctx.attr._template[GoDepInfo].targets,
    )
    source = go.library_to_source(go, attr, library, ctx.coverage_instrumented())
    archive, executable, runfiles = go.binary(
        go = go,
        name = ctx.attr.name,
        source = source,
    )
    return [
        library,
        source,
        archive,
        OutputGroupInfo(
            compilation_outputs = [archive.data.file],
        ),
        DefaultInfo(
            files = depset([executable]),
            runfiles = runfiles,
            executable = executable,
        ),
    ]

_go_busybox = go_transition_rule(
    attrs = {
        "cmds": attr.label_list(
            mandatory = True,
            allow_rules = ["go_busybox_library"],
        ),
        "_template": attr.label(
            providers = [GoArchive, GoDepInfo],
            allow_rules = ["go_binary"],
            aspects = [go_dep_aspect],
            default = Label("//pkg/bb/bbmain/cmd"),
        ),
        "_make_main": attr.label(
            executable = True,
            cfg = "host",
            allow_files = True,
            default = Label("//cmd/makebbmain"),
        ),
        "_go_context_data": attr.label(
            default = "@io_bazel_rules_go//:go_context_data",
        ),
    },
    executable = True,
    implementation = _go_busybox_impl,
    toolchains = ["@io_bazel_rules_go//go:toolchain"],
)

def go_busybox(name, cmds = [], **kwargs):
    rewrittenCmds = []
    cmd_names = []
    for c in cmds:
        cl = Label(c)
        if cl.name in cmd_names:
            fail("Two commands have the same name '%s'" % cl.name)

        go_busybox_library(
            name = "%s_%s" % (name, cl.name),
            cmd = c,
        )
        rewrittenCmds.append(":%s_%s" % (name, cl.name))

    _go_busybox(
        name = name,
        cmds = rewrittenCmds,
        **kwargs
    )
