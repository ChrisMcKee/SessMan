import { useCallback, useEffect, useMemo, useState } from "react";
import "./App.css";
import awsLogo from "./assets/images/Amazon_Web_Services_Logo.svg";
import {
  AddIntegration,
  ConnectSSM,
  DeleteIntegration,
  EKSSetupStatus,
  GetSettings,
  ListEKSClusters,
  ListIntegrations,
  ListSessions,
  ListSSMInstances,
  Login,
  Logout,
  SetPinned,
  SetProfileName,
  SSMPluginStatus,
  StartSession,
  StopSession,
  UpdateEKSKubeconfig,
  UpdateIntegration,
  UpdateSettings,
} from "../wailsjs/go/main/App";
import { EventsOn, Quit, WindowMinimise } from "../wailsjs/runtime/runtime";
import type { domain, eksmgr, ssmmgr } from "../wailsjs/go/models";

type IntegrationForm = {
  name: string;
  startUrl: string;
  ssoRegion: string;
  defaultRegion: string;
};

type DeviceCodeInfo = {
  userCode: string;
  verificationUri: string;
};

type NavView = "all" | "pinned" | string;

const AWS_REGIONS = [
  "af-south-1",
  "ap-east-1",
  "ap-northeast-1",
  "ap-northeast-2",
  "ap-northeast-3",
  "ap-south-1",
  "ap-south-2",
  "ap-southeast-1",
  "ap-southeast-2",
  "ap-southeast-3",
  "ap-southeast-4",
  "ca-central-1",
  "ca-west-1",
  "eu-central-1",
  "eu-central-2",
  "eu-north-1",
  "eu-south-1",
  "eu-south-2",
  "eu-west-1",
  "eu-west-2",
  "eu-west-3",
  "il-central-1",
  "me-central-1",
  "me-south-1",
  "sa-east-1",
  "us-east-1",
  "us-east-2",
  "us-west-1",
  "us-west-2",
];

const emptyForm: IntegrationForm = {
  name: "",
  startUrl: "",
  ssoRegion: "eu-west-1",
  defaultRegion: "eu-west-1",
};

function App() {
  const [integrations, setIntegrations] = useState<domain.Integration[]>([]);
  const [sessions, setSessions] = useState<domain.Session[]>([]);
  const [settings, setSettings] = useState<domain.Settings | null>(null);
  const [nav, setNav] = useState<NavView>("all");
  const [filter, setFilter] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [form, setForm] = useState<IntegrationForm>(emptyForm);
  const [showSettings, setShowSettings] = useState(false);
  const [profileEditId, setProfileEditId] = useState<string | null>(null);
  const [profileDraft, setProfileDraft] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deviceCode, setDeviceCode] = useState<DeviceCodeInfo | null>(null);
  const [ssmSession, setSsmSession] = useState<domain.Session | null>(null);
  const [ssmRegion, setSsmRegion] = useState("");
  const [ssmInstances, setSsmInstances] = useState<ssmmgr.Instance[]>([]);
  const [ssmLoading, setSsmLoading] = useState(false);
  const [ssmSetup, setSsmSetup] = useState<ssmmgr.SetupStatus | null>(null);
  const [ssmFilter, setSsmFilter] = useState("");
  const [eksSession, setEksSession] = useState<domain.Session | null>(null);
  const [eksRegion, setEksRegion] = useState("");
  const [eksClusters, setEksClusters] = useState<eksmgr.Cluster[]>([]);
  const [eksLoading, setEksLoading] = useState(false);
  const [eksSetup, setEksSetup] = useState<eksmgr.SetupStatus | null>(null);
  const [eksFilter, setEksFilter] = useState("");
  const [eksMessage, setEksMessage] = useState<string | null>(null);

  const refresh = useCallback(async (): Promise<boolean> => {
    try {
      const [integs, sess, sett] = await Promise.all([
        ListIntegrations(),
        ListSessions(),
        GetSettings(),
      ]);
      setIntegrations(integs ?? []);
      setSessions(sess ?? []);
      setSettings(sett);
      setNav((prev) => {
        if (prev === "all" || prev === "pinned") return prev;
        if ((integs ?? []).some((i) => i.id === prev)) return prev;
        return "all";
      });
      return true;
    } catch (e) {
      setError(String(e));
      return false;
    }
  }, []);

  function flashInfo(message: string) {
    setInfo(message);
    window.setTimeout(() => {
      setInfo((current) => (current === message ? null : current));
    }, 2000);
  }

  async function refreshFromButton() {
    setError(null);
    setBusy("Refreshing…");
    try {
      if (await refresh()) {
        flashInfo("Refreshed");
      }
    } finally {
      setBusy(null);
    }
  }

  useEffect(() => {
    refresh();
    EventsOn("workspace:updated", () => refresh());
    EventsOn("session:updated", () => refresh());
    EventsOn("auth:required", (id: string) => {
      setNav(id);
      setDeviceCode(null);
      setBusy("SSO expired — waiting for browser login…");
      setError(null);
      void (async () => {
        try {
          await Login(id);
          await refresh();
          setBusy(null);
          setDeviceCode(null);
        } catch (e) {
          const msg = String(e);
          // Another Login (e.g. the user already clicked) owns the UI.
          if (msg.toLowerCase().includes("already in progress")) {
            return;
          }
          setError(msg);
          setBusy(null);
          setDeviceCode(null);
        }
      })();
    });
    EventsOn("auth:device-code", (payload: DeviceCodeInfo) => {
      setDeviceCode(payload);
    });
  }, [refresh]);

  const selectedIntegration =
    nav !== "all" && nav !== "pinned"
      ? integrations.find((i) => i.id === nav) ?? null
      : null;

  const filteredSessions = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return sessions.filter((s) => {
      if (nav === "pinned" && !s.pinned) return false;
      if (nav !== "all" && nav !== "pinned" && s.integrationId !== nav) return false;
      if (!q) return true;
      return (
        s.accountName.toLowerCase().includes(q) ||
        s.accountId.toLowerCase().includes(q) ||
        s.roleName.toLowerCase().includes(q) ||
        s.profileName.toLowerCase().includes(q)
      );
    });
  }, [sessions, nav, filter]);

  async function withBusy(label: string, fn: () => Promise<void>) {
    setBusy(label);
    setError(null);
    try {
      await fn();
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(null);
    }
  }

  function openAdd() {
    setEditId(null);
    const region = settings?.defaultRegion || emptyForm.defaultRegion;
    setForm({ ...emptyForm, ssoRegion: region, defaultRegion: region });
    setShowAdd(true);
  }

  function openEdit(integ: domain.Integration) {
    setEditId(integ.id);
    setForm({
      name: integ.name,
      startUrl: integ.startUrl,
      ssoRegion: integ.ssoRegion,
      defaultRegion: settings?.defaultRegion || integ.ssoRegion,
    });
    setShowAdd(true);
  }

  async function saveIntegration() {
    await withBusy("Saving integration…", async () => {
      const payload = {
        name: form.name,
        startUrl: form.startUrl,
        ssoRegion: form.ssoRegion,
      };
      if (editId) {
        await UpdateIntegration(editId, payload);
      } else {
        const created = await AddIntegration(payload);
        setNav(created.id);
      }
      // Session list region comes from Settings.DefaultRegion, not SSO region.
      if (settings && form.defaultRegion !== settings.defaultRegion) {
        await UpdateSettings({ ...settings, defaultRegion: form.defaultRegion });
      }
      setShowAdd(false);
    });
  }

  async function saveSettings() {
    if (!settings) return;
    await withBusy("Saving settings…", async () => {
      await UpdateSettings(settings);
      setShowSettings(false);
    });
  }

  async function doLogin(integrationId: string) {
    setDeviceCode(null);
    setBusy("Waiting for browser login…");
    setError(null);
    try {
      await Login(integrationId);
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(null);
      setDeviceCode(null);
    }
  }

  function integrationName(id: string) {
    return integrations.find((i) => i.id === id)?.name ?? "AWS";
  }

  async function refreshSSMSetup(): Promise<ssmmgr.SetupStatus | null> {
    try {
      const status = await SSMPluginStatus();
      setSsmSetup(status);
      return status;
    } catch {
      setSsmSetup(null);
      return null;
    }
  }

  async function openSSM(sess: domain.Session) {
    setSsmSession(sess);
    setSsmInstances([]);
    setSsmFilter("");
    const region = sess.region || settings?.defaultRegion || "eu-west-1";
    setSsmRegion(region);
    const status = await refreshSSMSetup();
    if (status?.ready) {
      await loadSSMInstances(sess.id, region);
    }
  }

  async function changeSSMRegion(region: string) {
    setSsmRegion(region);
    if (!ssmSession) return;
    if (ssmSetup && !ssmSetup.ready) return;
    await loadSSMInstances(ssmSession.id, region);
  }

  const filteredSSMInstances = useMemo(() => {
    const q = ssmFilter.trim().toLowerCase();
    if (!q) return ssmInstances;
    return ssmInstances.filter((inst) => {
      return (
        inst.name?.toLowerCase().includes(q) ||
        inst.instanceId?.toLowerCase().includes(q) ||
        inst.platformName?.toLowerCase().includes(q) ||
        inst.platformType?.toLowerCase().includes(q) ||
        inst.ipAddress?.toLowerCase().includes(q)
      );
    });
  }, [ssmInstances, ssmFilter]);

  const ssmRegionOptions = useMemo(() => {
    const extras = [ssmRegion, ssmSession?.region, settings?.defaultRegion].filter(
      (r): r is string => !!r && !AWS_REGIONS.includes(r)
    );
    return [...extras, ...AWS_REGIONS];
  }, [ssmRegion, ssmSession?.region, settings?.defaultRegion]);

  async function loadSSMInstances(sessionId: string, region: string) {
    setSsmLoading(true);
    setError(null);
    try {
      const list = await ListSSMInstances(sessionId, region);
      setSsmInstances(list ?? []);
      await refresh();
    } catch (e) {
      setError(String(e));
      setSsmInstances([]);
    } finally {
      setSsmLoading(false);
    }
  }

  async function connectSSMInstance(instanceId: string) {
    if (!ssmSession) return;
    const status = await refreshSSMSetup();
    if (!status?.ready) {
      setError("Install the missing SSM tools below, then try Connect again.");
      return;
    }
    await withBusy("Opening terminal…", () =>
      ConnectSSM(ssmSession.id, ssmRegion, instanceId)
    );
  }

  async function refreshEKSSetup(): Promise<eksmgr.SetupStatus | null> {
    try {
      const status = await EKSSetupStatus();
      setEksSetup(status);
      return status;
    } catch {
      setEksSetup(null);
      return null;
    }
  }

  async function openEKS(sess: domain.Session) {
    setEksSession(sess);
    setEksClusters([]);
    setEksFilter("");
    setEksMessage(null);
    const region = sess.region || settings?.defaultRegion || "eu-west-1";
    setEksRegion(region);
    const status = await refreshEKSSetup();
    if (status?.ready) {
      await loadEKSClusters(sess.id, region);
    }
  }

  async function changeEKSRegion(region: string) {
    setEksRegion(region);
    setEksMessage(null);
    if (!eksSession) return;
    if (eksSetup && !eksSetup.ready) return;
    await loadEKSClusters(eksSession.id, region);
  }

  async function loadEKSClusters(sessionId: string, region: string) {
    setEksLoading(true);
    setError(null);
    setEksMessage(null);
    try {
      const list = await ListEKSClusters(sessionId, region);
      setEksClusters(list ?? []);
      await refresh();
    } catch (e) {
      setError(String(e));
      setEksClusters([]);
    } finally {
      setEksLoading(false);
    }
  }

  async function updateKubeconfig(clusterName: string) {
    if (!eksSession) return;
    const status = await refreshEKSSetup();
    if (!status?.ready) {
      setError("Install the AWS CLI, then try again.");
      return;
    }
    await withBusy("Updating kubeconfig…", async () => {
      await UpdateEKSKubeconfig(eksSession.id, eksRegion, clusterName);
      setEksMessage(`kubeconfig updated for ${clusterName}`);
    });
  }

  const filteredEKSClusters = useMemo(() => {
    const q = eksFilter.trim().toLowerCase();
    if (!q) return eksClusters;
    return eksClusters.filter((c) => {
      return (
        c.name?.toLowerCase().includes(q) ||
        c.status?.toLowerCase().includes(q) ||
        c.version?.toLowerCase().includes(q) ||
        c.arn?.toLowerCase().includes(q)
      );
    });
  }, [eksClusters, eksFilter]);

  const eksRegionOptions = useMemo(() => {
    const extras = [eksRegion, eksSession?.region, settings?.defaultRegion].filter(
      (r): r is string => !!r && !AWS_REGIONS.includes(r)
    );
    return [...extras, ...AWS_REGIONS];
  }, [eksRegion, eksSession?.region, settings?.defaultRegion]);

  const ssoRegionOptions = useMemo(() => {
    const extras = [form.ssoRegion, form.defaultRegion].filter(
      (r): r is string => !!r && !AWS_REGIONS.includes(r)
    );
    return [...new Set(extras), ...AWS_REGIONS];
  }, [form.ssoRegion, form.defaultRegion]);

  const defaultRegionOptions = useMemo(() => {
    const extras = [settings?.defaultRegion].filter(
      (r): r is string => !!r && !AWS_REGIONS.includes(r)
    );
    return [...extras, ...AWS_REGIONS];
  }, [settings?.defaultRegion]);

  return (
    <div className="app">
      <aside className="sidebar">
        <nav className="nav">
          <button
            type="button"
            className={nav === "all" ? "nav-item active" : "nav-item"}
            onClick={() => setNav("all")}
          >
            <ListIcon />
            All Sessions
          </button>
          <button
            type="button"
            className={nav === "pinned" ? "nav-item active" : "nav-item"}
            onClick={() => setNav("pinned")}
          >
            <PinIcon filled={false} />
            Pinned
          </button>
        </nav>

        <div className="sidebar-section">
          <div className="section-label">Integrations</div>
          <ul className="integ-list">
            {integrations.map((i) => (
              <li key={i.id}>
                <button
                  type="button"
                  className={nav === i.id ? "nav-item active" : "nav-item"}
                  onClick={() => setNav(i.id)}
                >
                  <AwsMark />
                  <span className="nav-text">
                    <span className="nav-title">{i.name}</span>
                    <span className={`nav-sub status-${i.status}`}>{i.status}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
          <button type="button" className="nav-item add" onClick={openAdd}>
            <PlusIcon />
            Add Integration
          </button>
        </div>
      </aside>

      <div className="content">
        <header className="topbar">
          <div className="topbar-left">
            <button
              type="button"
              className="icon-btn"
              title="Add integration"
              onClick={openAdd}
            >
              <PlusIcon />
            </button>
          </div>
          <div className="search-wrap">
            <SearchIcon />
            <input
              type="search"
              placeholder="Search session"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          </div>
          <div className="topbar-right">
            <button
              type="button"
              className="icon-btn"
              title="Refresh"
              onClick={() => refreshFromButton()}
            >
              <RefreshIcon />
            </button>
            <button
              type="button"
              className="icon-btn"
              title="Settings"
              onClick={() => setShowSettings(true)}
            >
              <GearIcon />
            </button>
            <div className="window-controls">
              <button
                type="button"
                className="win-btn"
                title="Minimise"
                onClick={() => WindowMinimise()}
              >
                <MinimiseIcon />
              </button>
              <button
                type="button"
                className="win-btn win-close"
                title="Close"
                onClick={() => Quit()}
              >
                <CloseIcon />
              </button>
            </div>
          </div>
        </header>

        {(error || deviceCode || busy || info) && (
          <div className="banners">
            {error && (
              <div className="banner error" role="alert">
                <span>{error}</span>
                <button type="button" className="ghost" onClick={() => setError(null)}>
                  Dismiss
                </button>
              </div>
            )}
            {deviceCode && (
              <div className="banner device-code" role="status">
                <div>
                  <div className="muted small">Confirm this code in the browser</div>
                  <div className="user-code">{deviceCode.userCode}</div>
                </div>
                <div className="muted small">Waiting for authorization…</div>
              </div>
            )}
            {busy && !deviceCode && <div className="banner info">{busy}</div>}
            {info && !busy && (
              <div className="banner info" role="status">
                <span>{info}</span>
                <button type="button" className="ghost" onClick={() => setInfo(null)}>
                  Dismiss
                </button>
              </div>
            )}
          </div>
        )}

        {selectedIntegration && (
          <div className="integ-bar">
            <div className="integ-bar-meta">
              <AwsMark large />
              <div>
                <div className="integ-bar-title">{selectedIntegration.name}</div>
                <div className="muted small">
                  {selectedIntegration.startUrl} · {selectedIntegration.ssoRegion}
                </div>
              </div>
            </div>
            <div className="integ-bar-actions">
              <button type="button" className="ghost" onClick={() => openEdit(selectedIntegration)}>
                Edit
              </button>
              {selectedIntegration.status === "LoggedIn" ? (
                <button
                  type="button"
                  onClick={() =>
                    withBusy("Logging out…", () => Logout(selectedIntegration.id))
                  }
                >
                  Logout
                </button>
              ) : (
                <button
                  type="button"
                  className="primary"
                  onClick={() => doLogin(selectedIntegration.id)}
                >
                  Login
                </button>
              )}
              <button type="button" className="danger" onClick={() => setConfirmDelete(true)}>
                Delete
              </button>
            </div>
          </div>
        )}

        <div className="table-scroll">
          {filteredSessions.length === 0 ? (
            <div className="empty-state">
              {integrations.length === 0 ? (
                <>
                  <h2>Add an AWS SSO integration</h2>
                  <p>
                    Provide your Identity Center start URL and region, then log in to
                    discover sessions and enable named AWS profiles.
                  </p>
                  <button type="button" className="primary" onClick={openAdd}>
                    Add Integration
                  </button>
                </>
              ) : nav === "pinned" ? (
                <p>No pinned sessions yet. Star a session to pin it here.</p>
              ) : selectedIntegration?.status !== "LoggedIn" ? (
                <p>Log in to this integration to discover account roles.</p>
              ) : (
                <p>No sessions match this view.</p>
              )}
            </div>
          ) : (
            <table className="sessions">
              <thead>
                <tr>
                  <th>Session</th>
                  <th>Identity</th>
                  <th>Provider</th>
                  <th>Named Profile</th>
                  <th>Region</th>
                  <th className="action-col">Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredSessions.map((s) => (
                  <tr key={s.id}>
                    <td>
                      <div className="session-cell">
                        <AwsMark />
                        <span className="session-name" title={s.accountId}>
                          {s.accountName}
                        </span>
                        <button
                          type="button"
                          className={s.pinned ? "star-btn on" : "star-btn"}
                          title={s.pinned ? "Unpin" : "Pin"}
                          onClick={async () => {
                            try {
                              await SetPinned(s.id, !s.pinned);
                              await refresh();
                            } catch (e) {
                              setError(String(e));
                            }
                          }}
                        >
                          <StarIcon filled={!!s.pinned} />
                        </button>
                      </div>
                    </td>
                    <td className="identity">{s.roleName}</td>
                    <td>
                      <span className="badge provider" title={integrationName(s.integrationId)}>
                        AWS Single Sign-On
                      </span>
                    </td>
                    <td>
                      {profileEditId === s.id ? (
                        <form
                          className="inline-form"
                          onSubmit={(e) => {
                            e.preventDefault();
                            withBusy("Updating profile…", async () => {
                              await SetProfileName(s.id, profileDraft);
                              setProfileEditId(null);
                            });
                          }}
                        >
                          <input
                            value={profileDraft}
                            onChange={(e) => setProfileDraft(e.target.value)}
                            autoFocus
                          />
                          <button type="submit">Save</button>
                          <button
                            type="button"
                            className="ghost"
                            onClick={() => setProfileEditId(null)}
                          >
                            Cancel
                          </button>
                        </form>
                      ) : (
                        <button
                          type="button"
                          className="profile-link"
                          title="Edit profile name"
                          onClick={() => {
                            setProfileEditId(s.id);
                            setProfileDraft(s.profileName);
                          }}
                        >
                          {s.profileName}
                        </button>
                      )}
                    </td>
                    <td>
                      <span className="badge region">
                        {(s.region || settings?.defaultRegion || "—").toUpperCase()}
                      </span>
                    </td>
                    <td className="action-col">
                      <div className="row-actions">
                        <button
                          type="button"
                          className="icon-btn"
                          title="Connect via SSM"
                          onClick={() => openSSM(s)}
                        >
                          <TerminalIcon />
                        </button>
                        <button
                          type="button"
                          className="icon-btn"
                          title="EKS kubeconfig"
                          onClick={() => openEKS(s)}
                        >
                          <EKSIcon />
                        </button>
                        <button
                          type="button"
                          className={
                            s.state === "Active"
                              ? "action-toggle on"
                              : s.state === "Pending"
                                ? "action-toggle pending"
                                : s.state === "Error"
                                  ? "action-toggle error"
                                  : "action-toggle"
                          }
                          title={
                            s.state === "Inactive"
                              ? "Start session"
                              : s.state === "Error"
                                ? s.lastError || "Stop session"
                                : "Stop session"
                          }
                          onClick={() =>
                            // Error/Pending are "on" leftovers (e.g. after cold start
                            // before auth resumes) — one click should clear them.
                            s.state === "Inactive"
                              ? withBusy("Starting…", () => StartSession(s.id))
                              : withBusy("Stopping…", () => StopSession(s.id))
                          }
                        />
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {ssmSession && (
        <div className="modal-backdrop" onClick={() => setSsmSession(null)}>
          <div className="modal modal-wide" onClick={(e) => e.stopPropagation()}>
            <h3>SSM Connect</h3>
            <p>
              Online instances for <strong>{ssmSession.accountName}</strong> using
              profile <code>{ssmSession.profileName}</code>.
            </p>

            {ssmSetup && !ssmSetup.ready && (
              <div className="setup-check">
                <div className="setup-check-title">Setup required</div>
                <p className="muted small">
                  Install the missing tools, then click Check again.
                </p>
                <ul className="setup-list">
                  {!ssmSetup.awsCli && (
                    <li>
                      <span>AWS CLI is not installed (or not on PATH)</span>
                      <a href={ssmSetup.awsCliUrl} target="_blank" rel="noreferrer">
                        Install guide
                      </a>
                    </li>
                  )}
                  {!ssmSetup.plugin && (
                    <li>
                      <span>Session Manager plugin is not installed (or not on PATH)</span>
                      <a href={ssmSetup.pluginUrl} target="_blank" rel="noreferrer">
                        Install guide
                      </a>
                    </li>
                  )}
                </ul>
                <button
                  type="button"
                  className="primary"
                  onClick={async () => {
                    const status = await refreshSSMSetup();
                    if (status?.ready && ssmSession) {
                      await loadSSMInstances(ssmSession.id, ssmRegion);
                    }
                  }}
                >
                  Check again
                </button>
              </div>
            )}

            {(!ssmSetup || ssmSetup.ready) && (
              <>
                <div className="ssm-toolbar">
                  <label className="ssm-region">
                    Region
                    <select
                      value={ssmRegion}
                      onChange={(e) => changeSSMRegion(e.target.value)}
                      disabled={ssmLoading}
                    >
                      {ssmRegionOptions.map((r) => (
                        <option key={r} value={r}>
                          {r}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="ssm-filter">
                    Filter
                    <input
                      type="search"
                      value={ssmFilter}
                      onChange={(e) => setSsmFilter(e.target.value)}
                      placeholder="Name, instance id, IP…"
                    />
                  </label>
                  <button
                    type="button"
                    onClick={() => loadSSMInstances(ssmSession.id, ssmRegion)}
                    disabled={ssmLoading}
                  >
                    {ssmLoading ? "Loading…" : "Refresh"}
                  </button>
                </div>
                <div className="ssm-table-wrap">
                  {ssmLoading ? (
                    <p className="muted">Loading instances…</p>
                  ) : filteredSSMInstances.length === 0 ? (
                    <p className="muted">
                      {ssmInstances.length === 0
                        ? "No online SSM instances in this region."
                        : "No instances match this filter."}
                    </p>
                  ) : (
                    <table className="ssm-table">
                      <thead>
                        <tr>
                          <th>Name</th>
                          <th className="col-instance-id">Instance ID</th>
                          <th>Platform</th>
                          <th>IP</th>
                          <th />
                        </tr>
                      </thead>
                      <tbody>
                        {filteredSSMInstances.map((inst) => (
                          <tr key={inst.instanceId}>
                            <td title={inst.name}>{inst.name}</td>
                            <td className="mono col-instance-id">{inst.instanceId}</td>
                            <td>{inst.platformName || inst.platformType || "—"}</td>
                            <td className="mono">{inst.ipAddress || "—"}</td>
                            <td>
                              <button
                                type="button"
                                className="primary"
                                onClick={() => connectSSMInstance(inst.instanceId)}
                              >
                                Connect
                              </button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              </>
            )}

            <div className="modal-actions">
              <button type="button" className="ghost" onClick={() => setSsmSession(null)}>
                Close
              </button>
            </div>
          </div>
        </div>
      )}

      {eksSession && (
        <div className="modal-backdrop" onClick={() => setEksSession(null)}>
          <div className="modal modal-wide" onClick={(e) => e.stopPropagation()}>
            <h3>EKS Clusters</h3>
            <p>
              Clusters for <strong>{eksSession.accountName}</strong> using profile{" "}
              <code>{eksSession.profileName}</code>. Update kubeconfig runs{" "}
              <code>aws eks update-kubeconfig</code>.
            </p>

            {eksSetup && !eksSetup.ready && (
              <div className="setup-check">
                <div className="setup-check-title">Setup required</div>
                <p className="muted small">
                  Install the AWS CLI, then click Check again.
                </p>
                <ul className="setup-list">
                  {!eksSetup.awsCli && (
                    <li>
                      <span>AWS CLI is not installed (or not on PATH)</span>
                      <a href={eksSetup.awsCliUrl} target="_blank" rel="noreferrer">
                        Install guide
                      </a>
                    </li>
                  )}
                </ul>
                <button
                  type="button"
                  className="primary"
                  onClick={async () => {
                    const status = await refreshEKSSetup();
                    if (status?.ready && eksSession) {
                      await loadEKSClusters(eksSession.id, eksRegion);
                    }
                  }}
                >
                  Check again
                </button>
              </div>
            )}

            {eksMessage && (
              <div className="banner info">
                <span>{eksMessage}</span>
                <button type="button" className="ghost" onClick={() => setEksMessage(null)}>
                  Dismiss
                </button>
              </div>
            )}

            {(!eksSetup || eksSetup.ready) && (
              <>
                <div className="ssm-toolbar">
                  <label className="ssm-region">
                    Region
                    <select
                      value={eksRegion}
                      onChange={(e) => changeEKSRegion(e.target.value)}
                      disabled={eksLoading}
                    >
                      {eksRegionOptions.map((r) => (
                        <option key={r} value={r}>
                          {r}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="ssm-filter">
                    Filter
                    <input
                      type="search"
                      value={eksFilter}
                      onChange={(e) => setEksFilter(e.target.value)}
                      placeholder="Name, status, version…"
                    />
                  </label>
                  <button
                    type="button"
                    onClick={() => loadEKSClusters(eksSession.id, eksRegion)}
                    disabled={eksLoading}
                  >
                    {eksLoading ? "Loading…" : "Refresh"}
                  </button>
                </div>
                <div className="ssm-table-wrap">
                  {eksLoading ? (
                    <p className="muted">Loading clusters…</p>
                  ) : filteredEKSClusters.length === 0 ? (
                    <p className="muted">
                      {eksClusters.length === 0
                        ? "No EKS clusters in this region."
                        : "No clusters match this filter."}
                    </p>
                  ) : (
                    <table className="ssm-table">
                      <thead>
                        <tr>
                          <th>Name</th>
                          <th>Status</th>
                          <th>Version</th>
                          <th />
                        </tr>
                      </thead>
                      <tbody>
                        {filteredEKSClusters.map((c) => (
                          <tr key={c.name}>
                            <td title={c.arn}>{c.name}</td>
                            <td>
                              <span className={`pill pill-${c.status === "ACTIVE" ? "Active" : "Pending"}`}>
                                {c.status || "—"}
                              </span>
                            </td>
                            <td className="mono">{c.version || "—"}</td>
                            <td>
                              <button
                                type="button"
                                className="primary"
                                disabled={c.status !== "ACTIVE"}
                                onClick={() => updateKubeconfig(c.name)}
                              >
                                Update kubeconfig
                              </button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              </>
            )}

            <div className="modal-actions">
              <button type="button" className="ghost" onClick={() => setEksSession(null)}>
                Close
              </button>
            </div>
          </div>
        </div>
      )}

      {confirmDelete && selectedIntegration && (
        <div className="modal-backdrop" onClick={() => setConfirmDelete(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <h3>Delete integration?</h3>
            <p>
              Remove <strong>{selectedIntegration.name}</strong> and its sessions?
              Active profiles for this integration will be stopped.
            </p>
            <div className="modal-actions">
              <button type="button" className="ghost" onClick={() => setConfirmDelete(false)}>
                Cancel
              </button>
              <button
                type="button"
                className="danger"
                onClick={() => {
                  setConfirmDelete(false);
                  withBusy("Deleting…", async () => {
                    await DeleteIntegration(selectedIntegration.id);
                    setNav("all");
                  });
                }}
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      )}

      {showAdd && (
        <div className="modal-backdrop" onClick={() => setShowAdd(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <h3>{editId ? "Edit integration" : "Add integration"}</h3>
            <label>
              Name
              <input
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="Work SSO"
              />
            </label>
            <label>
              Start URL
              <input
                value={form.startUrl}
                onChange={(e) => setForm({ ...form, startUrl: e.target.value })}
                placeholder="https://d-xxxxxxxxxx.awsapps.com/start"
              />
            </label>
            <label>
              SSO region
              <select
                value={form.ssoRegion}
                onChange={(e) => {
                  const ssoRegion = e.target.value;
                  setForm((prev) => ({
                    ...prev,
                    ssoRegion,
                    // Keep default region in lockstep until the user diverges.
                    defaultRegion:
                      prev.defaultRegion === prev.ssoRegion ? ssoRegion : prev.defaultRegion,
                  }));
                }}
              >
                {ssoRegionOptions.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Default region
              <select
                value={form.defaultRegion}
                onChange={(e) => setForm({ ...form, defaultRegion: e.target.value })}
              >
                {ssoRegionOptions.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </label>
            <p className="muted small" style={{ marginTop: "-0.5rem" }}>
              SSO region is for Identity Center. Default region is written on sessions and
              AWS profiles.
            </p>
            <div className="modal-actions">
              <button type="button" className="ghost" onClick={() => setShowAdd(false)}>
                Cancel
              </button>
              <button type="button" className="primary" onClick={saveIntegration}>
                Save
              </button>
            </div>
          </div>
        </div>
      )}

      {showSettings && settings && (
        <div className="modal-backdrop" onClick={() => setShowSettings(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <h3>Settings</h3>
            <label>
              Default region
              <select
                value={settings.defaultRegion}
                onChange={(e) =>
                  setSettings({ ...settings, defaultRegion: e.target.value })
                }
              >
                {defaultRegionOptions.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Rotation interval (minutes)
              <input
                type="number"
                min={5}
                value={settings.rotationIntervalMin}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    rotationIntervalMin: Number(e.target.value) || 20,
                  })
                }
              />
            </label>
            <label className="check-field">
              <input
                type="checkbox"
                checked={settings.syncProfileRegion}
                onChange={(e) =>
                  setSettings({
                    ...settings,
                    syncProfileRegion: e.target.checked,
                  })
                }
              />
              <span>
                <span className="check-title">Sync region to AWS config</span>
                <span className="check-help">
                  When starting a session, write the region into ~/.aws/config for that profile.
                </span>
              </span>
            </label>
            <div className="modal-actions">
              <button type="button" className="ghost" onClick={() => setShowSettings(false)}>
                Cancel
              </button>
              <button type="button" className="primary" onClick={saveSettings}>
                Save
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function AwsMark({ large }: { large?: boolean }) {
  return (
    <img
      src={awsLogo}
      alt=""
      className={large ? "aws-logo large" : "aws-logo"}
      draggable={false}
    />
  );
}

function StarIcon({ filled }: { filled: boolean }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden>
      <path
        d="M12 3.5l2.4 4.9 5.4.8-3.9 3.8.9 5.4L12 15.8 7.2 18.4l.9-5.4L4.2 9.2l5.4-.8L12 3.5z"
        fill={filled ? "currentColor" : "none"}
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function PinIcon({ filled }: { filled: boolean }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden fill={filled ? "currentColor" : "none"} stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 17v5" />
      <path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V17h14v-1.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V6a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1z" />
    </svg>
  );
}

function ListIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden>
      <path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" />
    </svg>
  );
}

function PlusIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden>
      <path d="M12 5v14M5 12h14" />
    </svg>
  );
}

function SearchIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden>
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" />
    </svg>
  );
}

function RefreshIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M21 12a9 9 0 1 1-2.6-6.2" />
      <path d="M21 3v6h-6" />
    </svg>
  );
}

function GearIcon() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 16 16"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden
      fill="currentColor"
    >
      <path d="m6.823 2-2.217.914.516 1.25a4.7 4.7 0 0 0-.95.944L2.925 4.59l-.922 2.212 1.247.52a4.8 4.8 0 0 0-.002 1.34L2 9.176l.914 2.218 1.248-.515a4.8 4.8 0 0 0 .945.949l-.518 1.247 2.214.921.519-1.246a5 5 0 0 0 .674.048 5 5 0 0 0 .666-.047L9.176 14l2.218-.914-.515-1.248a4.8 4.8 0 0 0 .95-.945l1.245.518.922-2.214-1.247-.519a4.7 4.7 0 0 0 .002-1.34L14 6.824l-.914-2.218-1.25.515a4.7 4.7 0 0 0-.944-.949l.518-1.246-2.212-.922-.52 1.247a5 5 0 0 0-.676-.049 5 5 0 0 0-.663.047Zm1.175 9a2.999 2.999 0 1 1 2.77-1.847 2.98 2.98 0 0 1-2.77 1.846M8 6.801a1.2 1.2 0 1 0 .46.093A1.2 1.2 0 0 0 8 6.8" />
    </svg>
  );
}

function MinimiseIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden>
      <path d="M2 6h8" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

function CloseIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden>
      <path
        d="M3 3l6 6M9 3l-6 6"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}

function TerminalIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M4 17 10 12 4 7" />
      <path d="M12 19h8" />
    </svg>
  );
}

function EKSIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 16 16"
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      aria-hidden
    >
      <path
        fill="url(#eks-paint0)"
        fillRule="evenodd"
        d="M6.381 10.148h.897V8.121l1.837 2.027h1.164L7.997 7.642l2.169-2.195H8.963L7.278 7.146V5.447h-.897v4.701z"
        clipRule="evenodd"
      />
      <path
        fill="url(#eks-paint1)"
        d="M8.532 3.803l3.186 1.81a.173.173 0 01.088.149v3.62c0 .06.033.118.088.149l2.842 1.615a.176.176 0 00.264-.15V3.947a.173.173 0 00-.088-.15L8.708.274a.176.176 0 00-.264.15v3.23c0 .062.034.119.088.15z"
      />
      <path
        fill="url(#eks-paint2)"
        d="M11.273 10.288l-3.185 1.81a.178.178 0 01-.176 0l-3.63-2.062a.173.173 0 01-.088-.15V5.762c0-.062.034-.119.088-.15l3.186-1.81a.172.172 0 00.088-.15V.424a.176.176 0 00-.264-.15L1.088 3.798a.173.173 0 00-.088.15V11.7c0 .061.033.118.088.15l6.824 3.876c.054.03.122.03.176 0l6.204-3.524a.172.172 0 000-.3l-2.843-1.615a.178.178 0 00-.176 0z"
      />
      <defs>
        <linearGradient
          id="eks-paint0"
          x1="10.691"
          x2="8.521"
          y1="9.879"
          y2="4.634"
          gradientUnits="userSpaceOnUse"
        >
          <stop stopColor="#426DDB" />
          <stop offset="1" stopColor="#3B4BDB" />
        </linearGradient>
        <linearGradient
          id="eks-paint1"
          x1="15.693"
          x2="9.546"
          y1="10.544"
          y2="-.213"
          gradientUnits="userSpaceOnUse"
        >
          <stop stopColor="#426DDB" />
          <stop offset="1" stopColor="#3B4BDB" />
        </linearGradient>
        <linearGradient
          id="eks-paint2"
          x1="9.433"
          x2="2.732"
          y1="14.904"
          y2="2.88"
          gradientUnits="userSpaceOnUse"
        >
          <stop stopColor="#2775FF" />
          <stop offset="1" stopColor="#188DFF" />
        </linearGradient>
      </defs>
    </svg>
  );
}

export default App;
