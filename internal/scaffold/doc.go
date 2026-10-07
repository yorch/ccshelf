// Package scaffold plans and writes the files of an org data repo for
// "ccshelf catalog init": a new repository in an empty directory, or the
// missing pieces of an existing marketplace repository.
//
// The work is split in three steps so that every command mode (dry run, the
// interactive confirmation, the real run) shows exactly what will happen:
//
//  1. Build reads the target through an FS and returns a Plan: one Entry per
//     generated file, each with an Action (create, skip-exists, needs-merge or
//     overwrite), the bytes to write and the reason. Build writes nothing.
//  2. The caller prints the plan.
//  3. Apply performs it. A file is created exclusively and atomically; an
//     existing file is never replaced unless the plan says overwrite, which
//     only happens with Params.Force and after the old bytes are saved as
//     <file>.bak.
//
// Safety (the spirit of SR1 to SR5): every write is confined to the target
// directory through os.Root, a symbolic link is never followed or written
// through (at the target, at an intermediate directory or at a file), .git is
// never touched, every user value is validated with a strict pattern and then
// written through an encoder for its format (TOML, JSON, Markdown) or, for the
// workflows, restricted to characters that need no quoting, and the output is
// deterministic: sorted, LF line endings, no timestamps, no clock reads.
//
// The package starts no process and makes no network call. R5 applies: the
// templates hold no organization names, hosts or data, and there are no
// built-in profiles; the only profile file is an all-comment sample that is
// written on request.
package scaffold
