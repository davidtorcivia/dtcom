const { defaults, preset, palettes, mountBackground } = await import(
  document.querySelector("script[data-background-renderer]").dataset
    .backgroundRenderer
);
const $ = (id) => document.getElementById("background-" + id);
const editor = document.querySelector(".background-editor");
let config = JSON.parse(editor.dataset.backgroundConfig) || defaults(),
  key = "desktopLight",
  preview = null,
  dirty = false,
  revision = 0;
config.presets ||= [];
const specs = [
  ["presence", "Presence", 0, 100],
  ["scale", "Scale", 45, 200],
  ["detail", "Structure", 0, 100],
  ["speed", "Motion", 0, 100],
  ["protect", "Reading space", 0, 100],
  ["grain", "Grain", 0, 100],
  ["filaments", "Filaments", 16, 320, 4],
  ["fiberWidth", "Line width", 25, 400, 4],
  ["fiberSpacing", "Line spacing", 40, 250, 4],
  ["lines", "Line density", 8, 100, 3],
  ["lineSize", "Line width", 25, 250, 3],
  ["lineSpacing", "Line spacing", 40, 250, 3],
];
const profile = () => config.profiles[key];
const isDark = () => key.endsWith("Dark");
function status(text, error = false) {
  $("status").textContent = text;
  $("status").dataset.error = error;
}
function change() {
  dirty = true;
  revision++;
  status("Unsaved changes");
  preview?.update();
}
function presets() {
  const selected = $("preset").value;
  $("preset").replaceChildren();
  for (const [value, label] of [
    ["1", "Overprint"],
    ["3", "Lensing"],
    ["4", "Filament"],
    ...config.presets.map((p, i) => ["custom:" + i, p.name]),
  ])
    $("preset").add(new Option(label, value));
  if ([...$("preset").options].some((o) => o.value === selected))
    $("preset").value = selected;
  $("delete").disabled = !$("preset").value.startsWith("custom:");
}
for (const p of palettes) $("palette").add(new Option(p.name, p.id));
$("palette").add(new Option("Custom", "custom"));
for (let i = 0; i < 3; i++) {
  const label = document.createElement("label");
  label.textContent = "Color " + (i + 1) + " ";
  const input = document.createElement("input");
  input.type = "color";
  input.id = "background-color-" + i;
  input.oninput = () => {
    profile().colors[i] = input.value;
    profile().palette = "custom";
    $("palette").value = "custom";
    change();
  };
  label.append(input);
  $("colors").append(label);
}
for (const [name, label, min, max, mode] of specs) {
  const el = document.createElement("label");
  el.className = "background-control";
  el.innerHTML =
    "<span>" +
    label +
    '<output id="background-value-' +
    name +
    '"></output></span><input type="range" id="background-' +
    name +
    '" min="' +
    min +
    '" max="' +
    max +
    '" step="' +
    (name === "filaments" ? 2 : 1) +
    '">';
  $("sliders").append(el);
  $(name).oninput = () => {
    profile()[name] = Number($(name).value);
    $("value-" + name).textContent = profile()[name];
    change();
  };
}
function fit() {
  const mobile = key.startsWith("mobile");
  const available = $("frame-wrap").getBoundingClientRect();
  const width = mobile
    ? Math.min(390, available.width)
    : Math.max(769, available.width);
  const scale = Math.min(1, available.width / width);
  const height = Math.round(available.height / scale);
  $("frame").style.cssText =
    `width:${width}px;height:${height}px;transform:scale(${scale});left:${Math.max(0, (available.width - width * scale) / 2)}px`;
  $("size").textContent = Math.round(width) + " × " + height;
}
function panel(open, focus = true) {
  $("panel").hidden = !open;
  $("launch").hidden = open;
  $("launch").setAttribute("aria-expanded", String(open));
  if (focus) (open ? $("minimize") : $("launch")).focus();
  try {
    sessionStorage.setItem("dt_background_panel", open ? "open" : "closed");
  } catch {}
}
$("minimize").onclick = () => panel(false);
$("launch").onclick = () => panel(true);
function escapePanel(e) {
  if (e.key === "Escape" && !$("panel").hidden) panel(false);
}
addEventListener("keydown", escapePanel);
try {
  panel(sessionStorage.getItem("dt_background_panel") !== "closed", false);
} catch {}
for (const button of document.querySelectorAll("[data-design]"))
  button.onclick = () => {
    $("design").value = button.dataset.design;
    $("design").dispatchEvent(new Event("change"));
  };

function sync() {
  const p = profile();
  document.documentElement.dataset.theme = isDark() ? "dark" : "light";
  document
    .querySelectorAll("[data-design]")
    .forEach((b) =>
      b.setAttribute(
        "aria-pressed",
        String(Number(b.dataset.design) === p.mode),
      ),
    );
  $("enabled").checked = config.enabled;
  $("design").value = p.mode;
  $("palette").value = p.palette;
  $("quality").value = p.quality;
  for (let i = 0; i < 3; i++) $("color-" + i).value = p.colors[i];
  for (const [name, , , , mode] of specs) {
    $(name).value = p[name];
    $("value-" + name).textContent = p[name];
    $(name).closest("label").hidden = !!mode && mode !== p.mode;
  }
  const doc = $("frame").contentDocument;
  if (doc?.body)
    doc.documentElement.dataset.theme = isDark() ? "dark" : "light";
  fit();
  preview?.update();
}
function loadPreview() {
  preview?.destroy();
  preview = null;
  const slug = $("page").value === "article" ? $("article").value.trim() : "";
  $("frame").src =
    "/admin/site/background/preview" +
    (slug ? "?article=" + encodeURIComponent(slug) : "");
}
function connectPreview() {
  preview?.destroy();
  preview = null;
  const doc = $("frame").contentDocument;
  if (!doc?.querySelector(".site-container")) {
    status("Preview unavailable. Check the article slug.", true);
    return;
  }
  doc.documentElement.dataset.theme = isDark() ? "dark" : "light";
  doc.addEventListener("keydown", escapePanel);
  doc.addEventListener("click", (e) => {
    if (e.target.closest("a")) e.preventDefault();
  });
  preview = mountBackground(doc, () => (config.enabled ? profile() : null));
  sync();
}
$("frame").addEventListener("load", connectPreview);
if (
  $("frame").contentDocument?.readyState === "complete" &&
  $("frame").contentDocument?.querySelector(".site-container")
)
  connectPreview();
$("context").onchange = () => {
  key = $("context").value;
  sync();
};
$("enabled").onchange = () => {
  config.enabled = $("enabled").checked;
  change();
};
$("design").onchange = () => {
  profile().mode = Number($("design").value);
  sync();
  change();
};
$("palette").onchange = () => {
  const id = $("palette").value;
  profile().palette = id;
  const p = palettes.find((p) => p.id === id);
  if (p)
    profile().colors = [
      ...((isDark() ? p.dark : p.light) || preset(profile().mode).colors),
    ];
  sync();
  change();
};
$("quality").onchange = () => {
  profile().quality = Number($("quality").value);
  change();
};
$("preset").onchange = () => {
  $("delete").disabled = !$("preset").value.startsWith("custom:");
};
$("apply").onclick = () => {
  const v = $("preset").value;
  config.profiles[key] = v.startsWith("custom:")
    ? structuredClone(config.presets[Number(v.split(":")[1])].profile)
    : preset(Number(v), isDark());
  sync();
  change();
};
$("store").onclick = () => {
  if (config.presets.length >= 20) {
    status("You can save up to 20 custom presets.", true);
    return;
  }
  const name = prompt("Name this custom preset:")?.trim();
  if (!name) return;
  if (
    new TextEncoder().encode(name).length > 60 ||
    config.presets.some((p) => p.name === name)
  ) {
    status("Use a unique name of 60 bytes or fewer.", true);
    return;
  }
  config.presets.push({ name, profile: structuredClone(profile()) });
  presets();
  $("preset").value = "custom:" + (config.presets.length - 1);
  $("delete").disabled = false;
  change();
};
$("delete").onclick = () => {
  const v = $("preset").value;
  if (!v.startsWith("custom:")) return;
  config.presets.splice(Number(v.split(":")[1]), 1);
  presets();
  change();
};
$("copy").onclick = () => {
  for (const k of Object.keys(config.profiles))
    config.profiles[k] = structuredClone(profile());
  change();
};
$("page").onchange = () => {
  $("article").hidden = $("page").value !== "article";
};
$("load").onclick = loadPreview;
$("form").onsubmit = async (e) => {
  e.preventDefault();
  const version = revision;
  $("save").disabled = true;
  status("Saving and rebuilding the siteâ€¦");
  try {
    const r = await fetch("/admin/site/background", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify(config),
    });
    if (!r.ok)
      throw new Error(
        r.status === 401
          ? "Your session expired. Sign in again in another tab, then retry."
          : await r.text(),
      );
    if (revision === version) {
      dirty = false;
      status("Saved. Backgrounds are live.");
    } else status("Saved. Your newer edits still need saving.");
  } catch (e) {
    status("Could not save: " + e.message, true);
  } finally {
    $("save").disabled = false;
  }
};
addEventListener("beforeunload", (e) => {
  if (dirty) {
    e.preventDefault();
    e.returnValue = "";
  }
});
new ResizeObserver(fit).observe($("frame-wrap"));
presets();
$("preset").value = String(profile().mode);
sync();
$("save").disabled = false;
