"use strict";

const state = {
  store: null,
  snapshot: null,
  filtered: [],
  productFiltered: [],
  page: 1,
  pageSize: 50,
  productPage: 1,
  productPageSize: 25,
  pollTimer: null,
};

const $ = (selector) => document.querySelector(selector);
const elements = {
  discoverForm: $("#discover-form"), storeURL: $("#store-url"), discoverButton: $("#discover-button"), notice: $("#notice"),
  scopePanel: $("#scope-panel"), storeCard: $("#store-card"), collections: $("#collections"), phrases: $("#title-phrases"),
  scanButton: $("#scan-button"), cancelButton: $("#cancel-button"), progressPanel: $("#progress-panel"),
  progressMessage: $("#progress-message"), progressNumbers: $("#progress-numbers"), progressBar: $("#progress-bar"),
  progressDiscovered: $("#progress-discovered"), progressRead: $("#progress-read"), progressMatched: $("#progress-matched"), progressErrors: $("#progress-errors"),
  dashboard: $("#dashboard"), snapshotCaption: $("#snapshot-caption"), exportButton: $("#export-button"), shopifyExportButton: $("#shopify-export-button"), kpiGrid: $("#kpi-grid"),
  productBody: $("#product-body"), productCount: $("#product-count"), productReset: $("#product-reset"),
  productPageSize: $("#product-page-size"), productPageInfo: $("#product-page-info"), productPagePrev: $("#product-page-prev"), productPageNext: $("#product-page-next"),
  productDialog: $("#product-dialog"), productDialogContent: $("#product-dialog-content"),
  variantBody: $("#variant-body"), tableCount: $("#table-count"), errorPanel: $("#error-panel"), errorCount: $("#error-count"), errorList: $("#error-list"),
  pageSize: $("#page-size"), pageInfo: $("#page-info"), pagePrev: $("#page-prev"), pageNext: $("#page-next"), resetFilters: $("#reset-filters"),
};

const filterIDs = ["filter-search", "filter-collection", "filter-vendor", "filter-type", "filter-stock", "filter-discount", "filter-min", "filter-max", "filter-sort"];
const productFilterIDs = ["product-search", "product-collection", "product-vendor", "product-type"];

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  });
  const contentType = response.headers.get("content-type") || "";
  const body = contentType.includes("application/json") ? await response.json() : await response.text();
  if (!response.ok) {
    throw new Error(body && body.error ? body.error : String(body || `HTTP ${response.status}`));
  }
  return body;
}

function showNotice(message, type = "") {
  elements.notice.textContent = message;
  elements.notice.className = `notice ${type}`.trim();
  elements.notice.hidden = !message;
}

function setLoading(button, loading) {
  button.disabled = loading;
  button.classList.toggle("loading", loading);
}

elements.discoverForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  clearTimeout(state.pollTimer);
  setLoading(elements.discoverButton, true);
  showNotice("Đang kiểm tra homepage, sitemap và currency…");
  try {
    const store = await api("/api/discover", { method: "POST", body: JSON.stringify({ store_url: elements.storeURL.value }) });
    state.store = store;
    elements.storeURL.value = store.store_url;
    renderStore(store);
    elements.scopePanel.hidden = false;
    showNotice(`Đã nhận diện ${store.product_count.toLocaleString("vi-VN")} product công khai.`, "success");
    elements.scopePanel.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    setLoading(elements.discoverButton, false);
  }
});

function renderStore(store) {
  const warnings = (store.warnings || []).length ? `<small>${esc(store.warnings.join(" · "))}</small>` : "";
  elements.storeCard.innerHTML = `
    <div class="store-icon">✓</div>
    <div><strong>${esc(store.name)}</strong><small>${esc(store.store_url)}</small>${warnings}</div>
    <div class="store-facts">
      <div><b>${Number(store.product_count).toLocaleString("vi-VN")}</b><span>products</span></div>
      <div><b>${Number((store.collections || []).length).toLocaleString("vi-VN")}</b><span>collections</span></div>
      <div><b>${esc(store.currency)}</b><span>currency</span></div>
    </div>`;
  elements.collections.innerHTML = "";
  for (const collection of store.collections || []) {
    const option = document.createElement("option");
    option.value = collection.handle;
    option.textContent = `${collection.title} (${collection.handle})`;
    elements.collections.appendChild(option);
  }
}

elements.scanButton.addEventListener("click", async () => {
  if (!state.store) return;
  elements.scanButton.disabled = true;
  elements.cancelButton.hidden = false;
  elements.progressPanel.hidden = false;
  const collections = [...elements.collections.selectedOptions].map((option) => option.value);
  const titlePhrases = elements.phrases.value.split(/[\n,;]+/).map((value) => value.trim()).filter(Boolean);
  try {
    await api("/api/scans", {
      method: "POST",
      body: JSON.stringify({ store_url: state.store.store_url, collections, title_phrases: titlePhrases }),
    });
    state.page = 1;
    state.productPage = 1;
    schedulePoll(0);
  } catch (error) {
    showNotice(error.message, "error");
    elements.scanButton.disabled = false;
    elements.cancelButton.hidden = true;
  }
});

elements.cancelButton.addEventListener("click", async () => {
  try {
    await api("/api/scans/current/cancel", { method: "POST", body: "{}" });
    elements.cancelButton.disabled = true;
    elements.progressMessage.textContent = "Đang hủy…";
  } catch (error) {
    showNotice(error.message, "error");
  }
});

function schedulePoll(delay = 700) {
  clearTimeout(state.pollTimer);
  state.pollTimer = setTimeout(pollScan, delay);
}

async function pollScan() {
  try {
    const snapshot = await api("/api/scans/current", { headers: {} });
    state.snapshot = snapshot;
    renderProgress(snapshot);
    renderDashboard(snapshot);
    if (snapshot.status === "running") {
      schedulePoll();
    } else {
      elements.scanButton.disabled = false;
      elements.cancelButton.hidden = true;
      elements.cancelButton.disabled = false;
      if (snapshot.status === "failed") showNotice(snapshot.error || "Scan thất bại", "error");
      if (snapshot.status === "completed") showNotice("Scan hoàn tất. Bạn có thể tải Excel phân tích hoặc CSV để import vào Shopify.", "success");
    }
  } catch (error) {
    showNotice(error.message, "error");
    elements.scanButton.disabled = false;
    elements.cancelButton.hidden = true;
  }
}

function renderProgress(snapshot) {
  const progress = snapshot.progress || {};
  const total = Number(progress.discovered || 0);
  const read = Number(progress.read || 0);
  const percent = total ? Math.min(100, read / total * 100) : snapshot.status === "completed" ? 100 : 4;
  elements.progressPanel.hidden = snapshot.status === "idle";
  elements.progressMessage.textContent = progress.message || "Đang chuẩn bị…";
  elements.progressNumbers.textContent = `${read.toLocaleString("vi-VN")} / ${total.toLocaleString("vi-VN")}`;
  elements.progressBar.style.width = `${percent}%`;
  elements.progressDiscovered.textContent = total.toLocaleString("vi-VN");
  elements.progressRead.textContent = read.toLocaleString("vi-VN");
  elements.progressMatched.textContent = Number(progress.matched || 0).toLocaleString("vi-VN");
  elements.progressErrors.textContent = Number(progress.errors || 0).toLocaleString("vi-VN");
}

function renderDashboard(snapshot) {
  if (snapshot.status === "idle") return;
  elements.dashboard.hidden = false;
  const summary = snapshot.summary || {};
  const currency = snapshot.store && snapshot.store.currency || "USD";
  const finished = snapshot.progress && snapshot.progress.finished_at ? new Date(snapshot.progress.finished_at).toLocaleString("vi-VN") : "đang quét";
  elements.snapshotCaption.textContent = `${snapshot.store.name || "Store"} · ${currency} · ${finished}`;
  elements.exportButton.classList.toggle("disabled", !["completed", "cancelled"].includes(snapshot.status));
  elements.exportButton.setAttribute("aria-disabled", String(!["completed", "cancelled"].includes(snapshot.status)));
  elements.shopifyExportButton.classList.toggle("disabled", !["completed", "cancelled"].includes(snapshot.status));
  elements.shopifyExportButton.setAttribute("aria-disabled", String(!["completed", "cancelled"].includes(snapshot.status)));

  const kpis = [
    ["Products", summary.products || 0, `${summary.variants || 0} variants · ${summary.images || 0} ảnh`, ""],
    ["Còn hàng", summary.available || 0, `${summary.unavailable || 0} hết hàng`, "green"],
    ["Đang giảm", summary.discounted || 0, "variants", "amber"],
    ["Giá thấp nhất", formatMoney(summary.min_price_minor || 0, currency), "", ""],
    ["Giá trung vị", formatMoney(summary.median_price_minor || 0, currency), "", ""],
    ["Lỗi / cảnh báo", `${summary.errors || 0} / ${summary.warnings || 0}`, "xem chi tiết bên dưới", summary.errors ? "red" : ""],
  ];
  elements.kpiGrid.innerHTML = kpis.map(([label, value, note, tone]) => `<article class="kpi ${tone ? `kpi--${tone}` : ""}"><span>${esc(label)}</span><strong>${esc(String(typeof value === "number" ? value.toLocaleString("vi-VN") : value))}</strong><small>${esc(note)}</small></article>`).join("");

  populateProductFilterOptions(snapshot.products || []);
  applyProductFilters();
  populateFilterOptions(snapshot.variants || []);
  applyFilters();
  drawCharts(snapshot.variants || [], summary, currency);
  renderErrors(snapshot.errors || []);
}

function populateProductFilterOptions(products) {
  setSelectOptions("#product-collection", unique(products.flatMap((product) => product.collections || [])));
  setSelectOptions("#product-vendor", unique(products.map((product) => product.vendor).filter(Boolean)));
  setSelectOptions("#product-type", unique(products.map((product) => product.product_type).filter(Boolean)));
}

function applyProductFilters() {
  if (!state.snapshot) return;
  const search = fold($("#product-search").value);
  const collection = $("#product-collection").value;
  const vendor = $("#product-vendor").value;
  const type = $("#product-type").value;
  const products = (state.snapshot.products || []).filter((product) => {
    const haystack = fold([
      product.title, product.handle, product.description_text, product.vendor, product.product_type,
      ...(product.tags || []), ...(product.collections || []), ...(product.options || []),
    ].join(" "));
    if (search && !haystack.includes(search)) return false;
    if (collection && !(product.collections || []).includes(collection)) return false;
    if (vendor && product.vendor !== vendor) return false;
    if (type && product.product_type !== type) return false;
    return true;
  });
  products.sort((a, b) => String(a.title).localeCompare(String(b.title), "vi", { sensitivity: "base" }));
  state.productFiltered = products;
  const pages = Math.max(1, Math.ceil(products.length / state.productPageSize));
  state.productPage = Math.min(state.productPage, pages);
  renderProductTable();
}

function renderProductTable() {
  const currency = state.snapshot.store.currency || "USD";
  const pages = Math.max(1, Math.ceil(state.productFiltered.length / state.productPageSize));
  const start = (state.productPage - 1) * state.productPageSize;
  const pageRows = state.productFiltered.slice(start, start + state.productPageSize);
  elements.productCount.textContent = `${state.productFiltered.length.toLocaleString("vi-VN")} sản phẩm`;
  elements.productPageInfo.textContent = `Trang ${state.productPage.toLocaleString("vi-VN")} / ${pages.toLocaleString("vi-VN")}`;
  elements.productPagePrev.disabled = state.productPage <= 1;
  elements.productPageNext.disabled = state.productPage >= pages;
  if (!pageRows.length) {
    elements.productBody.innerHTML = '<tr><td class="empty-row" colspan="8">Không có sản phẩm khớp bộ lọc.</td></tr>';
    return;
  }
  elements.productBody.innerHTML = pageRows.map((product) => {
    const imageURL = safeURL(product.image_url);
    const productURL = safeURL(product.url);
    const description = product.description_text || "Chưa có mô tả công khai.";
    const priceRange = product.min_price_minor === product.max_price_minor
      ? formatMoney(product.min_price_minor, currency)
      : `${formatMoney(product.min_price_minor, currency)} – ${formatMoney(product.max_price_minor, currency)}`;
    const stock = product.available === true ? '<span class="badge badge--yes">Còn hàng</span>' : product.available === false ? '<span class="badge badge--no">Hết hàng</span>' : '<span class="badge badge--unknown">Không rõ</span>';
    const image = imageURL === "#"
      ? '<span class="product-thumb product-thumb--empty">No image</span>'
      : `<img class="product-thumb" src="${esc(imageURL)}" alt="${esc(product.title)}" loading="lazy">`;
    return `<tr>
      <td>${image}</td>
      <td><strong>${esc(product.title)}</strong><small>${esc(product.handle || "—")} · ID: ${esc(product.product_id)}</small><small>${stock}</small></td>
      <td><p class="description-preview">${esc(description)}</p><button class="text-button" type="button" data-product-id="${esc(product.product_id)}">Xem chi tiết</button></td>
      <td><strong>${esc(product.vendor || "—")}</strong><small>${esc(product.product_type || "—")}</small><small>${esc((product.options || []).join(" · ") || "Không có option")}</small></td>
      <td><strong>${esc((product.tags || []).join(", ") || "—")}</strong><small>${esc((product.collections || []).join(", ") || "—")}</small></td>
      <td><strong>${Number(product.variant_count || 0).toLocaleString("vi-VN")} variants</strong><small>${Number(product.available_count || 0).toLocaleString("vi-VN")} còn hàng</small><small>${Number(product.image_count || 0).toLocaleString("vi-VN")} ảnh</small></td>
      <td class="price">${esc(priceRange)}</td>
      <td><div class="link-group"><a class="table-link" href="${esc(productURL)}" target="_blank" rel="noreferrer" title="Mở sản phẩm">↗</a>${imageURL !== "#" ? `<a class="table-link" href="${esc(imageURL)}" target="_blank" rel="noreferrer" title="Mở ảnh">▧</a>` : ""}</div></td>
    </tr>`;
  }).join("");
}

function showProductDetails(product) {
  const productURL = safeURL(product.url);
  const gallery = (product.images || []).map((image) => {
    const imageURL = safeURL(image.url);
    if (imageURL === "#") return "";
    const dimensions = image.width && image.height ? `${image.width} × ${image.height}` : "Không rõ kích thước";
    return `<a class="gallery-item" href="${esc(imageURL)}" target="_blank" rel="noreferrer">
      <img src="${esc(imageURL)}" alt="${esc(image.alt || product.title)}" loading="lazy">
      <span>${image.featured ? "Ảnh đại diện · " : ""}${esc(image.alt || `Ảnh ${image.position || ""}`)}<small>${esc(dimensions)}</small></span>
    </a>`;
  }).join("");
  elements.productDialogContent.innerHTML = `
    <div class="product-dialog__heading">
      <div><p class="eyebrow-dialog">PRODUCT DETAIL</p><h2>${esc(product.title)}</h2><p>${esc(product.handle || "")}</p></div>
      <a class="button button--primary" href="${esc(productURL)}" target="_blank" rel="noreferrer">Mở sản phẩm ↗</a>
    </div>
    <div class="product-meta">
      <span><b>Vendor</b>${esc(product.vendor || "—")}</span><span><b>Product type</b>${esc(product.product_type || "—")}</span>
      <span><b>Published</b>${esc(formatDate(product.published_at))}</span><span><b>Created</b>${esc(formatDate(product.created_at))}</span>
      <span><b>Variants</b>${Number(product.variant_count || 0).toLocaleString("vi-VN")}</span><span><b>Images</b>${Number(product.image_count || 0).toLocaleString("vi-VN")}</span>
    </div>
    <section class="dialog-section"><h3>Mô tả</h3><p class="product-description">${esc(product.description_text || "Chưa có mô tả công khai.")}</p></section>
    <section class="dialog-section"><h3>Tags & collections</h3><p>${esc((product.tags || []).join(", ") || "Không có tag")}</p><p>${esc((product.collections || []).join(", ") || "Không xác định collection")}</p></section>
    <section class="dialog-section"><h3>Hình ảnh (${Number(product.image_count || 0).toLocaleString("vi-VN")})</h3><div class="product-gallery">${gallery || '<p class="muted-copy">Sản phẩm không có ảnh công khai.</p>'}</div></section>`;
  if (typeof elements.productDialog.showModal === "function") elements.productDialog.showModal();
  else elements.productDialog.setAttribute("open", "");
}

function populateFilterOptions(rows) {
  setSelectOptions("#filter-collection", unique(rows.flatMap((row) => row.collections || [])));
  setSelectOptions("#filter-vendor", unique(rows.map((row) => row.vendor).filter(Boolean)));
  setSelectOptions("#filter-type", unique(rows.map((row) => row.product_type).filter(Boolean)));
}

function setSelectOptions(selector, values) {
  const select = $(selector);
  const current = select.value;
  select.innerHTML = '<option value="">Tất cả</option>' + values.map((value) => `<option value="${esc(value)}">${esc(value)}</option>`).join("");
  if (values.includes(current)) select.value = current;
}

function applyFilters() {
  if (!state.snapshot) return;
  const search = fold($("#filter-search").value);
  const collection = $("#filter-collection").value;
  const vendor = $("#filter-vendor").value;
  const type = $("#filter-type").value;
  const stock = $("#filter-stock").value;
  const discount = $("#filter-discount").value;
  const minPrice = parseOptionalNumber($("#filter-min").value);
  const maxPrice = parseOptionalNumber($("#filter-max").value);
  const sort = $("#filter-sort").value;
  const rows = (state.snapshot.variants || []).filter((row) => {
    const haystack = fold([row.product_title, row.variant_title, row.option1, row.option2, row.option3, row.sku, row.barcode, row.vendor, row.product_type, ...(row.tags || []), ...(row.collections || [])].join(" "));
    if (search && !haystack.includes(search)) return false;
    if (collection && !(row.collections || []).includes(collection)) return false;
    if (vendor && row.vendor !== vendor) return false;
    if (type && row.product_type !== type) return false;
    if (stock === "available" && row.available !== true) return false;
    if (stock === "unavailable" && row.available !== false) return false;
    if (stock === "unknown" && row.available !== null) return false;
    if (discount === "yes" && !(row.discount_minor > 0)) return false;
    if (discount === "no" && row.discount_minor > 0) return false;
    const price = Number(row.price_minor || 0) / 100;
    if (minPrice !== null && price < minPrice) return false;
    if (maxPrice !== null && price > maxPrice) return false;
    return true;
  });
  rows.sort((a, b) => {
    if (sort === "price-asc") return a.price_minor - b.price_minor;
    if (sort === "price-desc") return b.price_minor - a.price_minor;
    if (sort === "discount-desc") return b.discount_minor - a.discount_minor;
    return String(a.product_title).localeCompare(String(b.product_title), "vi", { sensitivity: "base" }) || String(a.variant_title).localeCompare(String(b.variant_title), "vi", { sensitivity: "base" });
  });
  state.filtered = rows;
  const pages = Math.max(1, Math.ceil(rows.length / state.pageSize));
  state.page = Math.min(state.page, pages);
  renderTable();
}

function renderTable() {
  const currency = state.snapshot.store.currency || "USD";
  const pages = Math.max(1, Math.ceil(state.filtered.length / state.pageSize));
  const start = (state.page - 1) * state.pageSize;
  const pageRows = state.filtered.slice(start, start + state.pageSize);
  elements.tableCount.textContent = `${state.filtered.length.toLocaleString("vi-VN")} variants`;
  elements.pageInfo.textContent = `Trang ${state.page.toLocaleString("vi-VN")} / ${pages.toLocaleString("vi-VN")}`;
  elements.pagePrev.disabled = state.page <= 1;
  elements.pageNext.disabled = state.page >= pages;
  if (!pageRows.length) {
    elements.variantBody.innerHTML = '<tr><td class="empty-row" colspan="10">Không có variant khớp bộ lọc.</td></tr>';
    return;
  }
  elements.variantBody.innerHTML = pageRows.map((row) => {
    const options = [row.option1, row.option2, row.option3].filter(Boolean).join(" · ");
    const compare = row.compare_at_minor == null ? "—" : formatMoney(row.compare_at_minor, currency);
    const discount = row.discount_minor > 0 ? `${formatMoney(row.discount_minor, currency)}<small>${((row.discount_pct || 0) * 100).toFixed(2)}%</small>` : "—";
    const stock = row.available === true ? '<span class="badge badge--yes">Còn hàng</span>' : row.available === false ? '<span class="badge badge--no">Hết hàng</span>' : '<span class="badge badge--unknown">Không rõ</span>';
    const productURL = safeURL(row.product_url);
    const imageURL = safeURL(row.image_url);
    return `<tr>
      <td><strong>${esc(row.product_title)}</strong><small>ID: ${esc(row.product_id)}</small></td>
      <td><strong>${esc(row.variant_title || "Default")}</strong><small>${esc(options || "Không có option")}</small></td>
      <td><strong>${esc(row.sku || "—")}</strong><small>${esc(row.barcode || "—")}</small></td>
      <td>${esc((row.collections || []).join(", ") || "—")}</td>
      <td class="price">${esc(formatMoney(row.price_minor, currency))}</td>
      <td class="compare-price">${esc(compare)}</td>
      <td class="discount">${discount}</td>
      <td>${stock}</td>
      <td><strong>${esc(row.vendor || "—")}</strong><small>${esc(row.product_type || "—")}</small></td>
      <td><div class="link-group"><a class="table-link" href="${esc(productURL)}" target="_blank" rel="noreferrer" title="Mở sản phẩm">↗</a>${imageURL !== "#" ? `<a class="table-link" href="${esc(imageURL)}" target="_blank" rel="noreferrer" title="Mở ảnh">▧</a>` : ""}</div></td>
    </tr>`;
  }).join("");
}

function renderErrors(errors) {
  elements.errorCount.textContent = errors.length.toLocaleString("vi-VN");
  elements.errorPanel.hidden = errors.length === 0;
  elements.errorList.innerHTML = errors.map((item) => `<div class="error-item ${item.warning ? "warning" : ""}"><strong>${item.warning ? "Cảnh báo" : "Lỗi"} · ${esc(item.stage)}</strong> — ${esc(item.message)}<code>${esc(item.url || "Không có URL")} ${item.status ? `· HTTP ${item.status}` : ""} · ${item.attempts || 0} lần thử</code></div>`).join("");
}

function drawCharts(rows, summary, currency) {
  drawPriceChart($("#price-chart"), rows, currency);
  drawStatusChart($("#status-chart"), summary);
}

function chartContext(canvas) {
  const ratio = window.devicePixelRatio || 1;
  const width = Math.max(320, canvas.clientWidth || 600);
  const height = 260;
  canvas.width = width * ratio;
  canvas.height = height * ratio;
  const context = canvas.getContext("2d");
  context.scale(ratio, ratio);
  context.clearRect(0, 0, width, height);
  context.font = "11px Segoe UI, sans-serif";
  return { context, width, height };
}

function drawPriceChart(canvas, rows, currency) {
  const { context: ctx, width, height } = chartContext(canvas);
  const prices = rows.map((row) => Number(row.price_minor || 0));
  if (!prices.length) return drawEmptyChart(ctx, width, height);
  let low = Number.POSITIVE_INFINITY, high = Number.NEGATIVE_INFINITY;
  for (const price of prices) {
    if (price < low) low = price;
    if (price > high) high = price;
  }
  const bucketCount = 6;
  const span = Math.max(1, high - low);
  const buckets = Array(bucketCount).fill(0);
  for (const price of prices) buckets[Math.min(bucketCount - 1, Math.floor((price - low) / span * bucketCount))]++;
  const maxCount = Math.max(...buckets, 1);
  const left = 48, bottom = 42, top = 20, chartWidth = width - left - 15, chartHeight = height - bottom - top;
  ctx.strokeStyle = "#dbe4ea"; ctx.beginPath(); ctx.moveTo(left, top); ctx.lineTo(left, top + chartHeight); ctx.lineTo(width - 10, top + chartHeight); ctx.stroke();
  const gap = 10, barWidth = Math.max(18, (chartWidth - gap * (bucketCount + 1)) / bucketCount);
  buckets.forEach((count, index) => {
    const barHeight = count / maxCount * (chartHeight - 20);
    const x = left + gap + index * (barWidth + gap), y = top + chartHeight - barHeight;
    ctx.fillStyle = "#2c8f70"; roundRect(ctx, x, y, barWidth, barHeight, 5); ctx.fill();
    ctx.fillStyle = "#17324d"; ctx.textAlign = "center"; ctx.fillText(String(count), x + barWidth / 2, Math.max(top + 11, y - 5));
    const bucketStart = low + span / bucketCount * index;
    ctx.fillStyle = "#64748b"; ctx.save(); ctx.translate(x + barWidth / 2, height - 13); ctx.rotate(-0.2); ctx.fillText(compactMoney(bucketStart, currency), 0, 0); ctx.restore();
  });
}

function drawStatusChart(canvas, summary) {
  const { context: ctx, width, height } = chartContext(canvas);
  const values = [
    ["Còn hàng", Number(summary.available || 0), "#2c8f70"],
    ["Hết hàng", Number(summary.unavailable || 0), "#bd3f49"],
    ["Không rõ", Number(summary.unknown_availability || 0), "#c57b20"],
    ["Đang giảm", Number(summary.discounted || 0), "#4e6f92"],
  ];
  const maxValue = Math.max(...values.map((item) => item[1]), 1);
  const left = 88, right = 28, top = 24, rowHeight = 52, maxWidth = width - left - right;
  values.forEach(([label, value, color], index) => {
    const y = top + index * rowHeight;
    ctx.fillStyle = "#64748b"; ctx.textAlign = "right"; ctx.fillText(label, left - 10, y + 18);
    ctx.fillStyle = "#edf1f2"; roundRect(ctx, left, y, maxWidth, 25, 6); ctx.fill();
    ctx.fillStyle = color; roundRect(ctx, left, y, Math.max(value ? 6 : 0, value / maxValue * maxWidth), 25, 6); ctx.fill();
    ctx.fillStyle = "#17324d"; ctx.textAlign = "left"; ctx.fillText(value.toLocaleString("vi-VN"), left + Math.min(maxWidth - 24, value / maxValue * maxWidth + 8), y + 17);
  });
}

function drawEmptyChart(ctx, width, height) {
  ctx.fillStyle = "#64748b"; ctx.textAlign = "center"; ctx.fillText("Chưa có dữ liệu", width / 2, height / 2);
}

function roundRect(ctx, x, y, width, height, radius) {
  if (width <= 0 || height <= 0) return;
  radius = Math.min(radius, width / 2, height / 2);
  ctx.beginPath();
  ctx.moveTo(x + radius, y);
  ctx.lineTo(x + width - radius, y);
  ctx.quadraticCurveTo(x + width, y, x + width, y + radius);
  ctx.lineTo(x + width, y + height - radius);
  ctx.quadraticCurveTo(x + width, y + height, x + width - radius, y + height);
  ctx.lineTo(x + radius, y + height);
  ctx.quadraticCurveTo(x, y + height, x, y + height - radius);
  ctx.lineTo(x, y + radius);
  ctx.quadraticCurveTo(x, y, x + radius, y);
  ctx.closePath();
}

for (const id of filterIDs) {
  $("#" + id).addEventListener("input", () => { state.page = 1; applyFilters(); });
  $("#" + id).addEventListener("change", () => { state.page = 1; applyFilters(); });
}
for (const id of productFilterIDs) {
  $("#" + id).addEventListener("input", () => { state.productPage = 1; applyProductFilters(); });
  $("#" + id).addEventListener("change", () => { state.productPage = 1; applyProductFilters(); });
}
elements.productPageSize.addEventListener("change", () => { state.productPageSize = Number(elements.productPageSize.value); state.productPage = 1; applyProductFilters(); });
elements.productPagePrev.addEventListener("click", () => { if (state.productPage > 1) { state.productPage--; renderProductTable(); } });
elements.productPageNext.addEventListener("click", () => { const pages = Math.ceil(state.productFiltered.length / state.productPageSize); if (state.productPage < pages) { state.productPage++; renderProductTable(); } });
elements.productReset.addEventListener("click", () => {
  for (const id of productFilterIDs) $("#" + id).value = "";
  state.productPage = 1;
  applyProductFilters();
});
elements.productBody.addEventListener("click", (event) => {
  const button = event.target.closest("[data-product-id]");
  if (!button || !state.snapshot) return;
  const product = (state.snapshot.products || []).find((item) => String(item.product_id) === button.dataset.productId);
  if (product) showProductDetails(product);
});
elements.pageSize.addEventListener("change", () => { state.pageSize = Number(elements.pageSize.value); state.page = 1; applyFilters(); });
elements.pagePrev.addEventListener("click", () => { if (state.page > 1) { state.page--; renderTable(); } });
elements.pageNext.addEventListener("click", () => { const pages = Math.ceil(state.filtered.length / state.pageSize); if (state.page < pages) { state.page++; renderTable(); } });
elements.resetFilters.addEventListener("click", () => {
  for (const id of filterIDs) $("#" + id).value = id === "filter-sort" ? "product-asc" : "";
  state.page = 1;
  applyFilters();
});
window.addEventListener("resize", () => { if (state.snapshot) drawCharts(state.snapshot.variants || [], state.snapshot.summary || {}, state.snapshot.store.currency || "USD"); });

function formatMoney(minor, currency) {
  const value = Number(minor || 0) / 100;
  if (!currency || currency === "UNKNOWN") return `${value.toLocaleString("vi-VN")} (không rõ tiền tệ)`;
  try { return new Intl.NumberFormat("vi-VN", { style: "currency", currency, maximumFractionDigits: zeroDecimal(currency) ? 0 : 2 }).format(value); }
  catch { return `${value.toLocaleString("vi-VN")} ${currency}`; }
}

function compactMoney(minor, currency) {
  const value = Number(minor || 0) / 100;
  try { return new Intl.NumberFormat("vi-VN", { notation: "compact", maximumFractionDigits: 1, style: "currency", currency: currency === "UNKNOWN" ? "USD" : currency }).format(value); }
  catch { return value.toLocaleString("vi-VN"); }
}

function formatDate(value) {
  if (!value) return "Không rõ";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString("vi-VN");
}

function zeroDecimal(currency) { return ["BIF","CLP","DJF","GNF","ISK","JPY","KMF","KRW","PYG","RWF","UGX","VND","VUV","XAF","XOF","XPF"].includes(currency); }
function parseOptionalNumber(value) { const trimmed = String(value).trim(); if (!trimmed) return null; const number = Number(trimmed); return Number.isFinite(number) ? number : null; }
function fold(value) { return String(value || "").toLocaleLowerCase("vi").normalize("NFD").replace(/[\u0300-\u036f]/g, "").replace(/đ/g, "d").replace(/\s+/g, " ").trim(); }
function unique(values) { return [...new Set(values)].sort((a, b) => String(a).localeCompare(String(b), "vi", { sensitivity: "base" })); }
function safeURL(value) { try { const parsed = new URL(value); return ["http:", "https:"].includes(parsed.protocol) ? parsed.href : "#"; } catch { return "#"; } }
function esc(value) { return String(value == null ? "" : value).replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[character])); }
