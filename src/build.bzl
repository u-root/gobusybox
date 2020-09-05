"""This module contains macros for building u-root busybox-style Go binaries.
Example usage:
  Let's say cmd/ls has an implementation of ls as a Go binary.
  Rather than writing a go_binary rule, we write a similar go_busybox_command
  rule, as seen below. You can use the same keywords as you would for
  go_binary.
  To compile this as a standalone binary not part of busybox, you can use the
  target //foo/bar/cmd/ls just like a go_binary target.
  go_busybox_command(
    name = "ls",
    srcs = [
      "ls.go",
      "ls_unix.go",
    ],
    deps = [
      "//vendor/github.com/.../humanize",
    ],
  )
  Do this for every command you want to include in your busybox binary.
  To create a busybox binary:
  go_busybox(
    name = "bb",
    commands = [
      "//foo/bar/cmd/ls",
      "//foo/bar/cmd/ip",
    ],
  )
  To create a CPIO initramfs of the busybox:
  nerf_initramfs(
      name = "initramfs",
      busybox = ":bb",
  )
"""

load("@io_bazel_rules_go//go:def.bzl", "go_binary", "go_context", "go_library", "go_rule")
load("@io_bazel_rules_go//go/private:providers.bzl", "GoArchive", "GoLibrary", "GoSource")

GoDepsInfo = provider("transitive_files")
CommandNamesInfo = provider("cmd_names")

def get_transitive_files(rulectx):
    directs = []
    transitives = []
    for d in rulectx.attr.deps:
        # Only taking the first two cuts out the .appengine.x. Find another
        # way to exclude it.
        directs += d.files.to_list()
        if GoDepsInfo in d:
            transitives.append(d[GoDepsInfo].transitive_files)
    return depset(directs, transitive = transitives)

def _go_dep_aspect(target, ctx):
    _ = target  # unused.
    tf = get_transitive_files(ctx.rule)
    return [GoDepsInfo(transitive_files = tf)]

# An aspect that collects all files produced by every "deps"-listed label in
# the tree.
go_dep_aspect = aspect(implementation = _go_dep_aspect)

def _uroot_rewrite_ast(ctx):
    """
    _uroot_rewrite_ast is the implementation of uroot_rewrite_ast.
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

    goc = go_context(ctx)
    for archive in goc.stdlib.libs:
        args.add("--archive", archive.path)

    inputs = get_transitive_files(ctx)
    for f in inputs.to_list():
        args.add("--archive", f.path)

    output_dir = None
    outputs = []
    for f in ctx.files.srcs:
        args.add("--source", f.path)

        # This relies on f.basename being relative to output_dir, which
        # they should be since they're relative to gen.. It's a
        # bit of a hack.
        outf = ctx.actions.declare_file("gen/%s" % f.basename)
        outputs += [outf]
        if not output_dir:
            output_dir = outf.dirname

    args.add("--dest_dir", output_dir)

    # Run the rewrite_ast binary.
    ctx.actions.run(
        inputs = depset(ctx.files.srcs, transitive = [inputs, depset(goc.stdlib.libs)]),
        outputs = outputs,
        arguments = [args],
        executable = ctx.executable._rewrite_ast,
    )

    # This makes the target usable as a stand-in for a set of files.
    return [DefaultInfo(files = depset(outputs))]

# Example usage:
#
# # Take all of ls' sources and rewrite them to be a library package.
# uroot_rewrite_ast(
#   name = "ls_uroot_rewrite",
#   # The last component of this package name must match the "package X"
#   # statement in ls.go.
#   package_name = "cmds/ls/main",
#   srcs = [
#      "ls.go",
#      "ls_unix.go",
#   ],
#   deps = [...],
# )
#
# go_library(
#   name = "ls_uroot",
#   srcs = [
#     # The rewrite rule provides all the source files we need.
#     ":ls_uroot_rewrite",
#   ],
#   deps = [...],
# )
uroot_rewrite_ast = go_rule(
    attrs = {
        "srcs": attr.label_list(
            mandatory = True,
            allow_files = True,
        ),
        "deps": attr.label_list(
            aspects = [go_dep_aspect],
            providers = [GoDepsInfo],
            allow_rules = ["go_library"],
        ),
        "package_name": attr.string(
            mandatory = True,
        ),
        "command_name": attr.string(
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
    implementation = _uroot_rewrite_ast,
)

def go_busybox_command(name, srcs, importpath, deps = [], **kwargs):
    """
    go_busybox_command builds a u-root busybox-native Go package.
    go_busybox_command rewrites a Go commands' source files to be a Go library,
    but also provides a target for a native standalone executable called
    {name}_standalone.
    The provided kwargs must work with both go_library and go_binary rules.
    Args:
        name: name of the command.
        srcs: set of source files to be compiled by this rule.
        deps: set of dependencies present in the source files.
        **kwargs: kwargs to use with the generated go_library and go_binary rules.
    """

    # Rewrite the source files to be a Go library package.
    uroot_rewrite_ast(
        name = "%s_uroot_rewrite" % name,
        package_name = "%s/main" % native.package_name(),
        srcs = srcs,
        command_name = name,
        # We need all dependencies to be built in order to use their type
        # information, which is read from the generated object files.
        #
        # TODO: convert this to use the GoArchive provider.
        deps = deps,
    )

    go_library(
        name = "%s_uroot" % name,
        # Use the source files generated by the above rule.
        srcs = [":%s_uroot_rewrite" % name],
        importpath = "%s_uroot" % importpath,
        deps = deps + [
            # We automagically add a dependency on the bb package.
            "//pkg/bb",
        ],
        **kwargs
    )

    # Also generate a standalone binary rule for the command.
    go_binary(
        name = name,
        srcs = srcs,
        deps = deps,
        pure = "on",
        **kwargs
    )

def _uroot_make_main_template(ctx):
    """
    _uroot_make_main implements the uroot_make_main rule.
    It takes a set of go_busybox_command dependencies to be compiled into one
    busybox binary and generates the appropriate main() package.
    TODO(chrisko): Don't force this to be colocated with the template files.
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

        f = ctx.actions.declare_file("%s_bbgen/%s" % (ctx.attr.name, f.basename))
        outputs += [f]
        if not output_dir:
            output_dir = f.dirname

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
    """
    Generates a busybox binary of many Go commands.
    This generates a busybox target binary :name, which strips all debug
    symbols, and a binary with debug symbols can be obtained using :name_debug.
    Args:
      name: binary name.
      commands: commands to include. Must be go_busybox_command macro
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
        deps = cmds + ["//pkg/bb"],
        **kwargs
    )

    go_binary(
        name = "%s_debug" % name,
        srcs = [":%s_gen_main" % name],
        pure = "on",
        deps = cmds + ["//pkg/bb"],
        **kwargs
    )
