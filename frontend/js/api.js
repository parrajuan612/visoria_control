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