// Simulador de carga inicial (Splash Screen)
window.addEventListener('load', () => {
    setTimeout(() => {
        document.getElementById('splash').classList.remove('step-active');
        document.getElementById('splash').classList.add('step-hidden');
        
        document.getElementById('step1').classList.remove('step-hidden');
        document.getElementById('step1').classList.add('step-active');
    }, 1500);
});

// Navegación entre pasos
async function goToStep(step) {
    if (step === 2) {
        // Antes de pasar al paso 2, cargamos la configuración maestra
        const btn = document.querySelector('#step1 button');
        const originalText = btn.innerHTML;
        btn.innerHTML = "Cargando configuración...";
        btn.disabled = true;

        try {
            // URL Pública de tu CSV de Google Sheets (Reemplaza si es diferente)
            const sheetUrl = "https://docs.google.com/spreadsheets/d/e/2PACX-1vTZRPVVDLaG_DrZbuk6FdMdgeATdckx8-juQNgpcdjG5yDpZ0XaVX5MjTMpNi7B5I1R2IUCV3WCdv-B/pub?output=csv";
            
            const response = await fetch(`${API_URL}/config/load`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ csv_url: sheetUrl })
            });

            if (!response.ok) throw new Error("No se pudo conectar con Google Sheets");

            console.log("Cerebro cargado con éxito.");
        } catch (error) {
            alert("Error al cargar configuración: " + error.message);
            btn.innerHTML = originalText;
            btn.disabled = false;
            return; // No lo dejamos avanzar si falla
        }
        
        btn.innerHTML = originalText;
        btn.disabled = false;
    }

    document.getElementById('step1').classList.replace('step-active', 'step-hidden');
    document.getElementById('step2').classList.replace('step-active', 'step-hidden');
    document.getElementById('step3').classList.replace('step-active', 'step-hidden');
    
    document.getElementById('step' + step).classList.replace('step-hidden', 'step-active');
}
// Variable global para guardar los niños mientras avanzamos por los pasos
window.currentPlayers = [];

// Paso 2: Subir Excel
document.getElementById('excelUpload').addEventListener('change', async (e) => {
    const file = e.target.files[0];
    if (!file) return;

    try {
        const data = await uploadPlayersExcel(file);
        
        // Guardamos los jugadores procesados
        window.currentPlayers = data.data;
        
        renderValidationTable(window.currentPlayers);
    } catch (error) {
        alert("Error: " + error.message);
    }
});

// Escuchar cuando el usuario arrastra/selecciona el Excel
document.getElementById('excelUpload').addEventListener('change', async (e) => {
    const file = e.target.files[0];
    if (!file) return;

    try {
        console.log("Subiendo archivo...");
        // Llamamos a la función de api.js
        const data = await uploadPlayersExcel(file);
        
        renderValidationTable(data.data);
    } catch (error) {
        alert("Error: " + error.message);
    }
});

// Dibuja la tabla en el HTML
function renderValidationTable(players) {
    const tableContainer = document.getElementById('validationTable');
    tableContainer.classList.remove('hidden');
    
    let html = `<table class="w-full text-left bg-white rounded shadow text-sm">
                    <thead class="bg-gray-100 text-gray-600">
                        <tr><th class="p-2">Jugador</th><th class="p-2">Torneo Asignado</th><th class="p-2">Estado</th></tr>
                    </thead><tbody>`;

players.forEach(p => {
        const isError = p.Status !== 'PENDING';
        const torneoNombre = p.Tournament && p.Tournament.Name ? p.Tournament.Name : '<span class="text-red-500 font-bold">Sin Torneo</span>';
        
        let statusText = 'Ok';
        if (p.Status === 'INVALID_DATA') statusText = 'Faltan Datos';
        if (p.Status === 'INVALID_MATCH') statusText = 'Error Beca/Año';

        html += `<tr class="border-b ${isError ? 'bg-red-50' : 'hover:bg-gray-50'}">
                    <td class="p-2">${p.Name}</td>
                    <td class="p-2 text-xs text-gray-500">${torneoNombre}</td>
                    <td class="p-2 font-bold ${isError ? 'text-red-500' : 'text-green-500'}">${statusText}</td>
                </tr>`;
    });

    html += `</tbody></table>`;
    tableContainer.innerHTML = html;
}

async function startMassiveProcess() {
    if (!window.currentPlayers || window.currentPlayers.length === 0) {
        alert("No hay jugadores válidos para procesar.");
        return;
    }

    const consoleDiv = document.getElementById('logConsole'); // Necesitamos este ID en el HTML
    const btn = document.getElementById('btnStartProcess');
    
    btn.disabled = true;
    btn.innerText = "Procesando... No cierres esta ventana";
    btn.classList.add('opacity-50', 'cursor-not-allowed');

    const log = (msg) => {
        if(consoleDiv) consoleDiv.innerHTML += `<p>> ${msg}</p>`;
        console.log(msg);
    };

    try {
        // 1. Generar PDFs
        log("Iniciando generación de PDFs en el servidor...");
        const pdfRes = await generatePDFs(window.currentPlayers);
        log(`Éxito: Se generaron ${pdfRes.generated_count} PDFs correctamente.`);

        // 2. Enviar WhatsApps
        log("-----------------------------------------");
        log("Iniciando envío de campaña por Meta API...");
        log("NOTA: Esto tomará tiempo para evitar baneos (aprox 3 seg por mensaje).");
        
        await sendWhatsAppMessages(window.currentPlayers);
        
        log("-----------------------------------------");
        log("✅ ¡PROCESO MASIVO COMPLETADO CON ÉXITO!");
        alert("¡Todos los certificados y mensajes han sido enviados!");

    } catch (error) {
        log(`<span class="text-red-500">❌ Error Crítico: ${error.message}</span>`);
        alert("Ocurrió un error en el proceso. Revisa la consola.");
    } finally {
        btn.innerText = "PROCESO FINALIZADO";
    }
}