import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { request } from "./api";
import { errorText, stored } from "./model";
import type { UpdateStatus } from "./model";

const dismissedKey = "atlas-update-dismissed";

async function waitForNewVersion(latest: string) {
  // Linux restarts in place; on Windows the installer closes this window and
  // relaunches the application, so this loop only matters on Linux.
  await new Promise((resolve) => setTimeout(resolve, 1500));
  for (let attempt = 0; attempt < 150; attempt++) {
    try {
      const status = await request<UpdateStatus>(
        "/api/update",
        undefined,
        false,
      );
      if (status.current === latest) break;
    } catch {
      /* the application is restarting */
    }
    await new Promise((resolve) => setTimeout(resolve, 400));
  }
  location.reload();
}

/** Offers a newer release; the user decides when to install it. */
export function UpdateBanner() {
  const update = useQuery({
    queryKey: ["update"],
    queryFn: () => request<UpdateStatus>("/api/update", undefined, false),
    staleTime: Infinity,
    retry: false,
  });
  const [dismissed, setDismissed] = useState(() =>
    stored<string>(dismissedKey, ""),
  );
  const [installing, setInstalling] = useState(false);
  const [error, setError] = useState("");
  const data = update.data;
  if (!data?.available || !data.latest || dismissed === data.latest)
    return null;
  const latest = data.latest;

  const install = async () => {
    if (
      !confirm(
        `Installer la version ${latest} ? L’application va redémarrer ; les transactions doivent être terminées.`,
      )
    )
      return;
    setInstalling(true);
    setError("");
    try {
      await request("/api/update/install", {});
      await waitForNewVersion(latest);
    } catch (failure) {
      setInstalling(false);
      setError(errorText(failure));
    }
  };

  return (
    <div className="session-warning update-banner" role="status">
      <span>
        {installing
          ? `Installation de la version ${latest}… l’application va redémarrer.`
          : `Nouvelle version ${latest} disponible (version actuelle ${data.current}).`}
        {error && <strong className="update-error"> {error}</strong>}
      </span>
      {!installing && (
        <span className="update-actions">
          {data.canInstall ? (
            <button className="button primary" onClick={() => void install()}>
              Installer et redémarrer
            </button>
          ) : (
            data.page && (
              <a
                className="button"
                href={data.page}
                target="_blank"
                rel="noreferrer"
              >
                Télécharger
              </a>
            )
          )}
          <button
            className="link-button"
            onClick={() => {
              try {
                localStorage.setItem(dismissedKey, JSON.stringify(latest));
              } catch {
                /* storage unavailable: hide for this page only */
              }
              setDismissed(latest);
            }}
          >
            Plus tard
          </button>
        </span>
      )}
    </div>
  );
}
