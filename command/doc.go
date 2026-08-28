// Package command runs bounded local shell commands and extracts compact evidence.
//
// # Trust model
//
// The runner executes a shell with the credentials of the embedding process.
// It is an execution policy engine, not a sandbox: nothing in this package
// restricts what a successfully started command may read, write, or contact.
//
// Trusted by the runner, because the embedding host controls them:
//
//   - the CommandPolicy (allowed working directories, timeouts, output and
//     history limits) supplied at construction;
//   - the command string itself: it is authored by the caller the host has
//     already decided to trust, and it runs verbatim after template
//     substitution;
//   - the host-provided ValueResolver and secret masks.
//
// Untrusted, and never safe to interpolate into a command:
//
//   - values substituted through [vars.Substitute]: stdin, template variables
//     and secret handles land in the command string before the shell parses
//     it, so a value containing quotes, semicolons, $( ) or backticks changes
//     the parsed command. This raw channel is legacy: hosts that accept
//     values from outside their trust boundary must use the injection-safe
//     channels instead -- [Runner.RunArgv] (values as argv elements),
//     [Exec.Env] (values as environment entries) and [Exec.Stdin] (values as
//     piped data) never become shell source. Raw substitution stays
//     available while it is being adopted, is documented as unsafe for
//     untrusted values, and is removed or gated before the module's first
//     stable version;
//   - anything a started command reads: files, environment, network.
//
// # Guarantees
//
// The runner does provide these properties:
//
//   - Working-directory containment: a command may only start in a directory
//     inside AllowedCWDs, checked against resolved real paths so a symlink
//     cannot point the working directory outside the allowlist. Containment
//     governs where a command starts, not what it may touch.
//   - Resource bounds: a per-run timeout with process-group termination,
//     bounded output retention, and bounded history records.
//   - Secret redaction: values registered on the runner and per-request
//     masks are removed from returned results, errors, callbacks and
//     persisted history. Redaction is best effort over retained output.
//
// # Advisory guards
//
// The textual policy checks -- protected-configuration references, protected
// registry source, and shell redirects into repository source files -- are
// UX guards against accidents, not a security boundary. They scan the command
// text for known patterns and are bypassable by construction: an interpreter,
// a path assembled at runtime, or a redirected variable all pass them. Do not
// rely on them to keep a hostile command away from a protected file; that
// requires not running the hostile command, or an OS-level sandbox applied
// outside this package.
package command
