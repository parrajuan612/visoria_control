const API_URL = 'http://localhost:8880/api/v1';

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
async function sendWhatsAppMessages(players) {
    const response = await fetch(`${API_URL}/messages/send`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ players: players })
    });

    if (!response.ok) {
        const errData = await response.json();
        throw new Error(errData.error || 'Error enviando mensajes de WhatsApp');
    }

    return await response.json();
}