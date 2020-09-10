"""This module contains macros for building u-root busybox-style Go binaries.

Example usage:

  Let's say cmd/ls has an implementation of ls as a Go binary.
  Rather than writing a go_binary rule, we write a similar go_busybox_library
  rule, as seen below. You can use the same keywords as you would for
  go_binary.
  To compile this as a standalone binary not part of busybox, you can use the
  target //foo/bar/cmd/ls just like a go_binary target.

  go_busybox_library(
    name = "ls_lib",
    srcs = [
      "ls.go",
      "ls_unix.go",
    ],
    deps = [
      "whatever",
    ],
  )

  go_binary(
    name = "ls",
    embed = [":ls_lib"],
  )

  Do this for every command you want to include in your busybox binary.
  To create a busybox binary:

  go_busybox_binary(
    name = "bb",
    commands = [
      "//foo/bar/cmd/ls_lib",
      "//foo/bar/cmd/ip_lib",
    ],
  )
"""

load("@io_bazel_rules_go//go:def.bzl", "go_binary", "go_context", "go_library", "go_rule")
load("@io_bazel_rules_go//go/private:providers.bzl", "GoArchive", "GoLibrary", "GoSource")

GoDepsInfo = provider("targets")
CommandNamesInfo = provider("cmd_names")

def _go_dep_aspect(target, ctx):
    targets = [target]
    for dep in ctx.rule.attr.deps:
        targets.append(dep)
        if GoDepsInfo in dep:
            targets.append(dep[GoDepsInfo].targets)
    return [GoDepsInfo(targets = targets)]

# An aspect that collects all recursive Target deps.
go_dep_aspect = aspect(implementation = _go_dep_aspect)

def __go_busybox_library(ctx):
    """Rewrite one Go command to be a library.

    It will take a go_binary's source files and rewrite them to be compatible
    with u-root's busybox mode as a library.

    Args:
      ctx: rule context

    Returns:
      The set of generated files which can be used with an
      attr.label_list(allow_files=True) (e.g. a go_library's srcs field).
    """
    args = ctx.actions.args()
    args.add("--name", ctx.attr.command_name)
    args.add("--package", ctx.attr.package_name)

    go = go_context(ctx)
    for archive in go.stdlib.libs:
        args.add("--archive", archive.path)

    args.add("--bb_import_path", "github.com/u-root/gobusybox/src/pkg/bb/bbmain")

    depInputs = []
    depTargets = []
    for dep in ctx.attr.deps:
        # Direct dependency
        arch = dep[GoArchive]
        args.add("--archive", "%s:%s" % (arch.data.importpath, arch.data.file.path))
        depInputs.append(arch.data.file)

        # This indistriminanetly adds *every* transitive dependency to the
        # top-level deps list. We could probably try to figure out which ones
        # are necessary from the output of the run action, but that seems
        # complicated, and bazel doesn't mind additional dependencies.
        depTargets += dep[GoDepsInfo].targets

        # Transitive dependencies of the direct dependency.
        for tdep in arch.transitive.to_list():
            args.add("--archive", "%s:%s" % (tdep.importpath, tdep.file.path))
            depInputs.append(tdep.file)

    output_dir = None
    outputs = []
    for f in ctx.files.srcs:
        args.add("--source", f.path)

        # This relies on f.basename being relative to output_dir, which
        # they should be since they're relative to gen.. It's a
        # bit of a hack.
        outf = go.actions.declare_file("gen/%s" % f.basename)
        outputs.append(outf)
        if not output_dir:
            output_dir = outf.dirname

    args.add("--dest_dir", output_dir)

    # Run the rewrite_ast binary.
    ctx.actions.run(
        inputs = depset(ctx.files.srcs, transitive = [depset(depInputs), depset(go.stdlib.libs)]),
        outputs = outputs,
        arguments = [args],
        executable = ctx.executable._rewrite_ast,
    )

    library = go.new_library(
        go = go,
        importpath = ctx.attr.importpath,
        srcs = outputs,
    )
    attr = struct(
        deps = ctx.attr.deps + depTargets,
    )
    source = go.library_to_source(go, attr, library, ctx.coverage_instrumented())
    archive = go.archive(go, source)
    return [library, source, archive, DefaultInfo(files = depset(outputs))]

_go_busybox_library = go_rule(
    attrs = {
        "srcs": attr.label_list(
            mandatory = True,
            allow_files = True,
        ),
        "deps": attr.label_list(
            providers = [GoArchive, GoDepsInfo],
            aspects = [go_dep_aspect],
        ),
        "package_name": attr.string(
            mandatory = True,
        ),
        "command_name": attr.string(
            mandatory = True,
        ),
        "importpath": attr.string(
            mandatory = True,
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
    },
    implementation = __go_busybox_library,
)

def go_busybox_library(name, srcs, importpath, deps = [], **kwargs):
    """go_busybox_library builds a u-root busybox-compatible Go package.

    Defines both a _uroot Go library, and a go_binary so it can be used as a
    drop in for go_binary.

    go_busybox_library rewrites a Go commands' source files to be a Go library.
    but also provides a target for a native standalone executable.

    The provided kwargs must work with both go_library and go_binary rules.

    Args:
        name: name of the command.
        srcs: set of source files to be compiled by this rule.
        importpath: Go import path for the package.
        deps: set of dependencies present in the source files.
        **kwargs: kwargs to use with the generated go_library and go_binary rules.
    """

    # Rewrite the source files to be a Go library package.
    _go_busybox_library(
        name = "%s_uroot" % name,
        package_name = "%s/main" % native.package_name(),
        srcs = srcs,
        command_name = name,
        importpath = importpath,
        deps = deps + ["//pkg/bb/bbmain"],
        **kwargs
    )

    # Also generate an embeddable binary rule for the command that go_binary
    # can use.
    go_library(
        name = name,
        srcs = srcs,
        deps = deps,
        importpath = importpath,
        **kwargs
    )

def _uroot_make_main_template(ctx):
    """_uroot_make_main creates main.go dispatcher for our Go busybox.

    It takes a set of go_busybox_library dependencies to be compiled into one
    busybox binary and generates the appropriate main() package.

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
    for dep in ctx.attr.cmds:
        args.add("--command", dep[GoLibrary].importpath)

    # Run the make_main binary.
    ctx.actions.run(
        inputs = inputs,
        outputs = outputs,
        arguments = [args],
        executable = ctx.executable._make_main,
    )

    # This makes the target usable as a stand-in for a set of files.
    return [
        DefaultInfo(files = depset(outputs)),
        CommandNamesInfo(cmd_names = ctx.attr.cmd_names),
    ]

uroot_make_main_template = go_rule(
    attrs = {
        "cmds": attr.label_list(
            mandatory = True,
            providers = [GoSource],
            allow_rules = ["go_library"],
        ),
        "cmd_names": attr.string_list(
            mandatory = True,
        ),
        "_template": attr.label(
            providers = [GoArchive],
            allow_rules = ["go_binary"],
            default = Label("//pkg/bb/bbmain/cmd"),
        ),
        "_make_main": attr.label(
            executable = True,
            cfg = "host",
            allow_files = True,
            default = Label("//cmd/makebbmain"),
        ),
    },
    implementation = _uroot_make_main_template,
)

def go_busybox_binary(name, commands = [], **kwargs):
    """Generates a busybox binary of many Go commands.

    This generates a busybox target binary :name, which strips all debug
    symbols, and a binary with debug symbols can be obtained using :name_debug.

    Args:
      name: binary name.
      commands: commands to include. Must be go_busybox_library macro
                invocations.
      **kwargs: additional arguments to pass to go_binary.
    """
    cmds = []
    cmd_names = []
    for c in commands:
        cl = Label(c)
        if cl.name in cmd_names:
            fail("Two commands have the same name '%s'" % cl.name)
        cmds.append("//%s:%s_uroot" % (cl.package, cl.name))
        cmd_names.append(cl.name)

    uroot_make_main_template(
        name = "%s_gen_main" % name,
        cmds = cmds,
        cmd_names = cmd_names,
    )

    go_binary(
        name = name,
        srcs = [":%s_gen_main" % name],
        # Strip all debug symbols.
        gc_linkopts = ["-s", "-w"],
        pure = "on",
        deps = cmds + ["//pkg/bb/bbmain"],
        **kwargs
    )

    go_binary(
        name = "%s_debug" % name,
        srcs = [":%s_gen_main" % name],
        pure = "on",
        deps = cmds + ["//pkg/bb/bbmain"],
        **kwargs
    )
