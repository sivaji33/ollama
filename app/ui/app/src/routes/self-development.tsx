import { useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { AppSidebar } from "@/components/AppSidebar";
import { SidebarLayout } from "@/components/layout/layout";
import {
  cancelSelfDevelopment,
  getSelfDevelopment,
  startSelfDevelopment,
} from "@/api";
import type { SelfDevelopmentDiff, SelfDevelopmentSnapshot } from "@/api";

export const Route = createFileRoute("/self-development")({
  component: SelfDevelopmentRoute,
});

function SelfDevelopmentRoute() {
  const [workspace, setWorkspace] = useState("");
  const [task, setTask] = useState("");
  const [verify, setVerify] = useState("go test ./...");
  const [build, setBuild] = useState("go build ./...");
  const [transaction, setTransaction] =
    useState<SelfDevelopmentSnapshot | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!transaction || !["PLANNED", "EXECUTING"].includes(transaction.state)) {
      return;
    }
    let stopped = false;
    const poll = async () => {
      try {
        const latest = await getSelfDevelopment(transaction.id);
        if (!stopped) {
          setTransaction(latest);
        }
      } catch (pollError) {
        if (!stopped) {
          setError(String(pollError));
        }
      }
    };
    const timer = window.setInterval(poll, 1000);
    void poll();
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [transaction?.id, transaction?.state]);

  const currentDiff = useMemo(() => {
    const cycles = transaction?.result?.cycles ?? [];
    const finalDiff = cycles[cycles.length - 1]?.filesystem_diff;
    if (finalDiff?.files?.length) {
      return finalDiff;
    }
    const eventDiffs = transaction?.events
      .map((event) => event.filesystem_diff)
      .filter((diff): diff is SelfDevelopmentDiff => Boolean(diff?.files?.length));
    return eventDiffs?.[eventDiffs.length - 1];
  }, [transaction]);

  const fileSummary = useMemo(() => {
    const files = currentDiff?.files.filter((file) => file.status !== "move_source") ?? [];
    const inspected = new Set(
      (transaction?.events ?? [])
        .filter(
          (event) =>
            event.stage === "inspection" &&
            event.tool_name === "read_file" &&
            event.status === "COMPLETED" &&
            event.path,
        )
        .map((event) => event.path),
    );
    const commands = (transaction?.events ?? []).filter(
      (event) =>
        (event.stage === "verification" || event.stage === "build") &&
        event.status === "EXECUTING",
    );
    return {
      inspected: inspected.size,
      created: files.filter((file) => file.status === "created").length,
      modified: files.filter((file) => file.status === "modified").length,
      deleted: files.filter((file) => file.status === "deleted").length,
      moved: files.filter((file) => file.status === "moved").length,
      commands: commands.length,
      tests: commands.filter((event) => /\btest\b/i.test(event.message)).length,
      builds: commands.filter((event) => event.stage === "build").length,
    };
  }, [currentDiff, transaction?.events]);

  const onSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError("");
    setBusy(true);
    setTransaction(null);
    try {
      const started = await startSelfDevelopment({
        workspace: workspace.trim() || undefined,
        task: task.trim(),
        verify: verify
          .split(/\r?\n/)
          .map((line) => line.trim())
          .filter(Boolean),
        build: build.trim() || undefined,
      });
      setTransaction(started);
    } catch (startError) {
      setError(String(startError));
    } finally {
      setBusy(false);
    }
  };

  const onCancel = async () => {
    if (!transaction) {
      return;
    }
    try {
      await cancelSelfDevelopment(transaction.id);
    } catch (cancelError) {
      setError(String(cancelError));
    }
  };

  return (
    <SidebarLayout
      title="Live Development"
      sidebar={<AppSidebar current="development" />}
    >
      <main className="mx-auto flex w-full max-w-5xl flex-col gap-6 p-6">
        <header>
          <h1 className="text-2xl font-semibold text-neutral-900 dark:text-neutral-100">
            Self-Development
          </h1>
          <p className="mt-2 text-sm text-neutral-600 dark:text-neutral-400">
            The local coding agent edits the selected live workspace. The
            backend checkpoints every changed file, calculates the filesystem
            diff, verifies it, then retains or restores the changes.
          </p>
        </header>

        <form
          className="flex flex-col gap-4 rounded-xl border border-neutral-200 p-4 dark:border-neutral-700"
          onSubmit={onSubmit}
        >
          <label className="flex flex-col gap-1 text-sm">
            Live workspace
            <input
              className="rounded-md border border-neutral-300 bg-transparent px-3 py-2 dark:border-neutral-600"
              value={workspace}
              onChange={(event) => setWorkspace(event.target.value)}
              placeholder="Server's current directory"
              disabled={busy || Boolean(transaction && ["PLANNED", "EXECUTING"].includes(transaction.state))}
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Task
            <textarea
              className="min-h-24 rounded-md border border-neutral-300 bg-transparent px-3 py-2 dark:border-neutral-600"
              value={task}
              onChange={(event) => setTask(event.target.value)}
              required
              disabled={busy || Boolean(transaction && ["PLANNED", "EXECUTING"].includes(transaction.state))}
              placeholder="Describe one focused source improvement"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Verification commands (one per line)
            <textarea
              className="min-h-16 rounded-md border border-neutral-300 bg-transparent px-3 py-2 font-mono text-xs dark:border-neutral-600"
              value={verify}
              onChange={(event) => setVerify(event.target.value)}
              disabled={busy || Boolean(transaction && ["PLANNED", "EXECUTING"].includes(transaction.state))}
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Build command
            <input
              className="rounded-md border border-neutral-300 bg-transparent px-3 py-2 font-mono text-xs dark:border-neutral-600"
              value={build}
              onChange={(event) => setBuild(event.target.value)}
              disabled={busy || Boolean(transaction && ["PLANNED", "EXECUTING"].includes(transaction.state))}
            />
          </label>
          <div className="flex items-center gap-3">
            <button
              className="rounded-md bg-neutral-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50 dark:bg-neutral-100 dark:text-neutral-900"
              type="submit"
              disabled={busy || !task.trim() || Boolean(transaction && ["PLANNED", "EXECUTING"].includes(transaction.state))}
            >
              {busy ? "Starting…" : "Start transaction"}
            </button>
            {transaction && ["PLANNED", "EXECUTING"].includes(transaction.state) && (
              <button
                className="rounded-md border border-neutral-300 px-4 py-2 text-sm dark:border-neutral-600"
                type="button"
                onClick={onCancel}
              >
                Cancel
              </button>
            )}
            {transaction && (
              <span className="font-mono text-xs text-neutral-500">
                {transaction.id}
              </span>
            )}
          </div>
        </form>

        {error && (
          <p className="rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">
            {error}
          </p>
        )}

        {transaction && (
          <>
            <section className="rounded-xl border border-neutral-200 p-4 dark:border-neutral-700">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <h2 className="font-semibold">Transaction activity</h2>
                  <p className="mt-1 text-xs text-neutral-500">
                    {transaction.task}
                  </p>
                </div>
                <span className="rounded-full border border-neutral-300 px-3 py-1 text-xs font-semibold dark:border-neutral-600">
                  {transaction.state}
                </span>
              </div>
              <dl className="mt-4 grid gap-2 text-xs sm:grid-cols-2">
                <div>
                  <dt className="text-neutral-500">Runtime</dt>
                  <dd className="font-mono">{transaction.runtime.endpoint}</dd>
                </div>
                <div>
                  <dt className="text-neutral-500">Model</dt>
                  <dd className="font-mono">{transaction.runtime.model}</dd>
                </div>
                <div>
                  <dt className="text-neutral-500">Runtime process</dt>
                  <dd className="font-mono">
                    {transaction.runtime.process_state}
                    {transaction.runtime.pid ? ` · PID ${transaction.runtime.pid}` : ""}
                  </dd>
                </div>
                <div>
                  <dt className="text-neutral-500">Runtime identity</dt>
                  <dd className="break-all font-mono">
                    {transaction.runtime.runtime_identity || transaction.runtime.executable}
                  </dd>
                </div>
                <div>
                  <dt className="text-neutral-500">Model / inference</dt>
                  <dd className="font-mono">
                    {transaction.runtime.model_availability} / {transaction.runtime.inference || "pending"}
                  </dd>
                </div>
                <div>
                  <dt className="text-neutral-500">Runtime diagnostic</dt>
                  <dd className="font-mono">
                    {transaction.runtime.failure_state ||
                      (transaction.runtime.inference === "passed" ? "healthy" : "verification pending")}
                  </dd>
                </div>
                <div className="sm:col-span-2">
                  <dt className="text-neutral-500">Workspace</dt>
                  <dd className="break-all font-mono">{transaction.workspace}</dd>
                </div>
              </dl>
              <dl className="mt-4 grid grid-cols-2 gap-3 rounded-lg bg-neutral-50 p-3 text-xs dark:bg-neutral-900 sm:grid-cols-4">
                <SummaryCount label="Files inspected" value={fileSummary.inspected} />
                <SummaryCount label="Files created" value={fileSummary.created} />
                <SummaryCount label="Files modified" value={fileSummary.modified} />
                <SummaryCount label="Files deleted" value={fileSummary.deleted} />
                <SummaryCount label="Files moved" value={fileSummary.moved} />
                <SummaryCount label="Commands" value={fileSummary.commands} />
                <SummaryCount label="Tests" value={fileSummary.tests} />
                <SummaryCount label="Builds" value={fileSummary.builds} />
              </dl>
              <ol className="mt-5 flex flex-col gap-3 border-l border-neutral-300 pl-4 dark:border-neutral-700">
                {transaction.events.map((event, index) => (
                  <li key={`${event.timestamp}-${index}`} className="relative">
                    <span className="absolute -left-[21px] top-0.5 h-2.5 w-2.5 rounded-full border border-neutral-400 bg-white dark:bg-neutral-900" />
                    <div className="flex flex-wrap items-baseline gap-x-2">
                      <span className="text-[10px] text-neutral-500">
                        {new Date(event.timestamp).toLocaleTimeString()}
                      </span>
                      <span className="text-xs font-semibold">
                        {event.status}
                      </span>
                      <span className="text-xs uppercase tracking-wide text-neutral-500">
                        {event.stage}
                      </span>
                    </div>
                    <p className="mt-0.5 break-all text-sm">
                      {event.message}
                      {event.path && (
                        <span className="ml-1 font-mono text-xs">{event.path}</span>
                      )}
                    </p>
                  </li>
                ))}
              </ol>
            </section>

            {transaction.error && (
              <p className="rounded-md border border-red-300 p-3 text-sm text-red-800 dark:border-red-900 dark:text-red-200">
                {transaction.error}
              </p>
            )}

            {currentDiff && <FilesystemDiffView diff={currentDiff} />}

            {transaction.result?.cycles.map((cycle) => (
              <section
                className="rounded-xl border border-neutral-200 p-4 text-sm dark:border-neutral-700"
                key={`${cycle.id}-${cycle.index}`}
              >
                <h2 className="font-semibold">
                  {cycle.outcome === "kept"
                    ? "Changes retained"
                    : cycle.outcome === "reverted"
                      ? "Changes rolled back"
                      : "No change applied"}
                </h2>
                {cycle.reason && <p className="mt-2">{cycle.reason}</p>}
                <p className="mt-2 text-xs text-neutral-500">
                  Files retained: {cycle.files_retained} · Files restored:{" "}
                  {cycle.files_restored}
                </p>
              </section>
            ))}
          </>
        )}
      </main>
    </SidebarLayout>
  );
}

function SummaryCount({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <dt className="text-neutral-500">{label}</dt>
      <dd className="mt-1 font-mono font-semibold">{value}</dd>
    </div>
  );
}

function FilesystemDiffView({ diff }: { diff: SelfDevelopmentDiff }) {
  const visibleFiles = diff.files.filter((file) => file.status !== "move_source");
  return (
    <section className="rounded-xl border border-neutral-200 p-4 dark:border-neutral-700">
      <header className="mb-4 flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="font-semibold">Live filesystem diff</h2>
        <p className="font-mono text-xs">
          {visibleFiles.length} files · +{diff.additions} / -{diff.deletions}
        </p>
      </header>
      <div className="flex flex-col gap-2">
        {visibleFiles.map((file) => (
          <details
            className="rounded-md border border-neutral-200 dark:border-neutral-700"
            key={`${file.old_path ?? ""}:${file.path}`}
            open
          >
            <summary className="cursor-pointer list-none px-3 py-2 text-sm">
              <span className="mr-2 text-neutral-500">{file.status}</span>
              {file.old_path && (
                <span className="mr-1 font-mono text-xs">{file.old_path} →</span>
              )}
              <span className="font-mono text-xs">{file.path}</span>
              <span className="float-right font-mono text-xs text-neutral-500">
                +{file.additions} / -{file.deletions}
              </span>
            </summary>
            <div className="grid border-t border-neutral-200 dark:border-neutral-700 md:grid-cols-2">
              <div className="min-w-0 border-b p-3 dark:border-neutral-700 md:border-b-0 md:border-r">
                <h3 className="mb-2 text-xs font-semibold uppercase text-neutral-500">
                  Before · {file.before_lines} lines
                </h3>
                <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words font-mono text-xs">
                  {file.before ?? ""}
                </pre>
              </div>
              <div className="min-w-0 p-3">
                <h3 className="mb-2 text-xs font-semibold uppercase text-neutral-500">
                  After · {file.after_lines} lines
                </h3>
                <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words font-mono text-xs">
                  {file.after ?? ""}
                </pre>
              </div>
            </div>
            <pre className="max-h-96 overflow-auto border-t border-neutral-200 bg-neutral-50 p-3 font-mono text-xs dark:border-neutral-700 dark:bg-neutral-950">
              {file.unified_diff}
            </pre>
          </details>
        ))}
      </div>
    </section>
  );
}
