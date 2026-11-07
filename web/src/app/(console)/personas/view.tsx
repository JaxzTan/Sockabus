"use client";
import Link from "next/link";
import { Avatar, Badge, Chip, Head, Spin } from "@/components/ui";
import { DEV_CHAT, useQuery } from "@/lib/hooks";
import { generatePersona, setPersonaStatus, useStore } from "@/lib/store";
import { PersonaDetailPanel } from "./detail";

const FILTERS = ["all", "active", "draft", "paused"] as const;

export function PersonasView() {
  const { data, ui } = useStore();
  const q = useQuery();
  const f = q.get("status", "all");
  const newId = q.get("new");
  const counts: Record<string, number> = { all: data.personas.length, active: 0, draft: 0, paused: 0 };
  data.personas.forEach((p) => {
    if (counts[p.status] != null) counts[p.status]++;
  });
  // Personas bound to a real number (e.g. Auntie Mei) go first; others keep their order.
  const live = (h: string) => (h === "web chat only" || h === "not bound" ? 1 : 0);
  const list = data.personas.filter((p) => f === "all" || p.status === f).sort((a, b) => live(a.handle) - live(b.handle));
  const np = newId ? data.personas.find((p) => p.id === newId) : undefined;
  const selId = q.get("selected");
  const sel = selId ? data.personas.find((p) => p.id === selId) : undefined;

  const generate = async () => {
    const id = await generatePersona();
    q.set({ status: "all", new: id });
  };

  const genBtn = ui.generating ? (
    <button className="btn btn-primary" disabled data-testid="persona-generate-loading">
      <Spin />
      Generating… up to 30 s
    </button>
  ) : (
    <button className="btn btn-primary" onClick={generate} data-testid="persona-generate-btn">
      + Generate persona
    </button>
  );

  let banner = null;
  if (ui.genError) {
    banner = (
      <div className="banner err" data-testid="persona-generate-error">
        <div className="txt">
          <h3>{ui.genErrorSetup ? "Persona generation isn't set up on the server" : "Couldn't generate a persona"}</h3>
          <p>
            {ui.genErrorSetup
              ? "The backend has no AI model key, so it can't write new personas. Nothing was saved. The existing personas still work for the demo."
              : `${ui.genError.replace(/\.?$/, ".")} Nothing was saved. Try again — it usually works on the second click.`}
          </p>
          {ui.genErrorSetup && (
            <p className="muted mono" style={{ fontSize: 14 }}>
              {ui.genError}
            </p>
          )}
        </div>
        {!ui.genErrorSetup && (
          <div className="acts">
            <button className="btn btn-primary" onClick={generate}>
              Try again
            </button>
          </div>
        )}
      </div>
    );
  } else if (np?.status === "draft") {
    banner = (
      <div className="banner" role="status">
        <Avatar p={np} lg />
        <div className="txt">
          <h3>
            New persona ready — {np.name}, {np.age} · {np.loc}
          </h3>
          <p>
            {np.backstory} {np.wealth} · speaks {np.langs.join(", ")}.
          </p>
          <p className="muted">
            Activating binds {np.name} to honeypot <span className="mono">{np.handle}</span>, so scammers can message right away.
          </p>
        </div>
        <div className="acts">
          <button className="btn btn-secondary" onClick={generate} disabled={ui.generating}>
            Generate another
          </button>
          <button className="btn btn-success" onClick={() => setPersonaStatus(np.id, "active")} data-testid="persona-activate-btn">
            Activate now
          </button>
        </div>
      </div>
    );
  } else if (np?.status === "active") {
    banner = (
      <div className="banner" style={{ background: "var(--green-soft)", borderColor: "var(--green)" }}>
        <div className="txt">
          <h3>
            {np.name} is live on <span className="mono">{np.handle}</span>
          </h3>
          <p>Next, play the scammer and send the first message.</p>
        </div>
        {DEV_CHAT && (
          <div className="acts">
            <Link className="btn btn-primary" href={`/demo/chat?persona=${np.id}`}>
              Chat as scammer →
            </Link>
          </div>
        )}
      </div>
    );
  }

  return (
    <>
      <Head
        title="Personas"
        sub={
          <>
            Fake victims that scammers believe are real. Only <b>active</b> personas can be reached.
          </>
        }
        right={genBtn}
      />
      {banner}
      {sel && <PersonaDetailPanel p={sel} onClose={() => q.set({ selected: null })} />}
      {selId && !sel && (
        <div className="card empty" data-testid="persona-detail">
          <b>Persona not found</b>
          <span>It may have been removed. Pick another one from the list.</span>
          <button className="btn btn-secondary btn-sm" onClick={() => q.set({ selected: null })}>
            Close
          </button>
        </div>
      )}
      <div className="tabs" role="group" aria-label="Filter by status">
        {FILTERS.map((k) => (
          <button key={k} aria-pressed={f === k} onClick={() => q.set({ status: k })}>
            {k[0].toUpperCase() + k.slice(1)} {counts[k]}
          </button>
        ))}
      </div>
      <div className="tablewrap">
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Languages</th>
              <th>Status</th>
              <th>Honeypot</th>
              <th>Created</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {list.length ? (
              list.map((p) => (
                <tr key={p.id} className={(p.id === newId && p.status === "draft") || p.id === selId ? "hl" : ""} data-testid="persona-row">
                  <td>
                    <div className="namecell">
                      <Avatar p={p} />
                      <div>
                        <b data-testid="persona-name">{p.name}</b>
                        <small>
                          {p.age} · {p.loc} · {p.occ}
                        </small>
                      </div>
                    </div>
                  </td>
                  <td>
                    <div className="row-gap">
                      {p.langs.map((l) => (
                        <Chip key={l}>{l}</Chip>
                      ))}
                    </div>
                  </td>
                  <td data-testid="persona-status">
                    <Badge word={p.status} />
                  </td>
                  <td className="mono" style={{ fontSize: 14 }}>
                    {p.handle}
                  </td>
                  <td className="muted">{p.created}</td>
                  <td className="right">
                    <div className="row-gap" style={{ justifyContent: "flex-end", flexWrap: "nowrap" }}>
                    <button
                      className="btn btn-quiet btn-sm"
                      onClick={() => q.set({ selected: p.id })}
                      aria-pressed={p.id === selId}
                      data-testid="persona-modify-btn"
                    >
                      Details
                    </button>
                    {p.status === "active" ? (
                      <button className="btn btn-secondary btn-sm" onClick={() => setPersonaStatus(p.id, "paused")} data-testid="persona-pause-btn">
                        Pause
                      </button>
                    ) : (
                      <button
                        className={`btn ${p.status === "draft" ? "btn-success" : "btn-secondary"} btn-sm`}
                        onClick={() => setPersonaStatus(p.id, "active")}
                        data-testid="persona-activate-btn"
                      >
                        Activate
                      </button>
                    )}
                    </div>
                  </td>
                </tr>
              ))
            ) : (
              <tr>
                <td colSpan={6}>
                  <div className="empty">
                    <b>No {f} personas</b>
                    <span>Generate one, or switch the filter to All.</span>
                  </div>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <p className="foot-note">
        Generated personas never use real people&apos;s names, IDs or photos. Lifecycle: draft → active → paused → retired. Every prompt edit is saved as a new
        version.
      </p>
    </>
  );
}
