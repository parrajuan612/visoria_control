const BASE_URL = window.location.origin;
const API_URL = `${BASE_URL}/api/v1`;
async function uploadPlayersExcel(file) {
    const formData = new FormData();
    formData.append('file', file);

    const response = await fetch(`${API_URL}/players/upload`, {
        method: 'POST',
        body: formData
    });

    if (!response.ok) {
        const errData = await response.json();
        throw new Error(errData.error || 'Error subiendo archivo');
    }

    return await response.json();
}
async function generatePDFs(players) {
    const response = await fetch(`${API_URL}/documents/generate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ players: players })
    });

    if (!response.ok) {
        const errData = await response.json();
        throw new Error(errData.error || 'Error generando PDFs');
    }

    return await response.json();
}
// Reemplaza o agrega esta función en tu js/api.js
// En js/api.js
function sendWhatsAppMessages(players, onProgress) {
    return new Promise((resolve, reject) => {
        fetch(`${API_URL}/messages/send`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ players: players })
        })
        .then(response => {
            if (!response.ok) throw new Error('Error enviando mensajes');
            
            const reader = response.body.getReader();
            const decoder = new TextDecoder("utf-8");
            
            function readStream() {
                reader.read().then(({ done, value }) => {
                    if (done) {
                        resolve();
                        return;
                    }
                    
                    const chunk = decoder.decode(value, { stream: true });
                    const lines = chunk.split('\n');
                    
                    for (let line of lines) {
                        if (line.startsWith('data:')) {
                            const msg = line.replace('data:', '').trim();
                            if (msg === "FIN") {
                                resolve();
                                return;
                            }
                            if (msg && onProgress) {
                                onProgress(msg);
                            }
                        }
                    }
                    // Seguimos leyendo el siguiente fragmento
                    readStream();
                }).catch(err => reject(err));
            }
            
            readStream();
        })
        .catch(err => reject(err));
    });
}
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

// Puente temporal: los destinatarios en el formato del flujo de envío actual.
function getCampaignPlayers(id) {
    return apiCall(`${API_URL}/campaigns/${encodeURIComponent(id)}/players`);
}
