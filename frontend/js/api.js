const BASE_URL = window.location.origin;
const API_URL = `${BASE_URL}/api/v1`;

// --- Configuración maestra (Google Sheets) ---
async function getConfig() {
    const response = await fetch(`${API_URL}/config`);
    if (!response.ok) throw new Error('No se pudo leer la configuración');
    return await response.json();
}

// Sincroniza la configuración. Devuelve { ok, status, data } sin lanzar por 4xx/5xx
// para que la pantalla pueda mostrar los hallazgos de una hoja inválida (422).
// url es opcional: si se envía y la hoja es válida, queda como nueva fuente.
async function syncConfig(url) {
    const options = { method: 'POST' };
    if (url) {
        options.headers = { 'Content-Type': 'application/json' };
        options.body = JSON.stringify({ url: url });
    }
    const response = await fetch(`${API_URL}/config/sync`, options);
    const data = await response.json().catch(() => ({}));
    return { ok: response.ok, status: response.status, data: data };
}

// --- Campañas (importación, validación y corrección de filas) ---
// Devuelven { ok, status, data } sin lanzar por 4xx/5xx: la pantalla muestra el mensaje del servidor.
async function apiCall(url, options) {
    const response = await fetch(url, options);
    const data = await response.json().catch(() => ({}));
    return { ok: response.ok, status: response.status, data: data };
}

// fields: { torneo, fecha_pago_1, fecha_pago_2, fecha_pago_3, requerir_ambos }
function createCampaign(file, fields) {
    const formData = new FormData();
    formData.append('file', file);
    for (const [k, v] of Object.entries(fields)) formData.append(k, v);
    return apiCall(`${API_URL}/campaigns`, { method: 'POST', body: formData });
}

function getCampaign(id) {
    return apiCall(`${API_URL}/campaigns/${encodeURIComponent(id)}`);
}

// patch: { correo: "x@y.com", movil: "320..." } (solo los campos que cambian)
function patchRecipient(campaignId, recipientId, patch) {
    return apiCall(`${API_URL}/campaigns/${encodeURIComponent(campaignId)}/recipients/${encodeURIComponent(recipientId)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch)
    });
}

// --- Documentos y envío ---
function generateDocuments(id) {
    return apiCall(`${API_URL}/campaigns/${encodeURIComponent(id)}/documents`, { method: 'POST' });
}

// channels: ['whatsapp', 'email'] (vacío = todos los configurados)
function dispatchCampaign(id, channels) {
    return apiCall(`${API_URL}/campaigns/${encodeURIComponent(id)}/dispatch`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ channels: channels || [] })
    });
}

function retryCampaign(id) {
    return apiCall(`${API_URL}/campaigns/${encodeURIComponent(id)}/retry`, { method: 'POST' });
}

function resumeCampaign(id) {
    return apiCall(`${API_URL}/campaigns/${encodeURIComponent(id)}/resume`, { method: 'POST' });
}

function getDeliveries(id) {
    return apiCall(`${API_URL}/campaigns/${encodeURIComponent(id)}/deliveries`);
}

// --- Plantillas Word ---
function listTemplates() {
    return apiCall(`${API_URL}/templates`);
}

function installTemplate(key, file) {
    const formData = new FormData();
    formData.append('file', file);
    return apiCall(`${API_URL}/templates/${encodeURIComponent(key)}`, { method: 'PUT', body: formData });
}
