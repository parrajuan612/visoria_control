// Simulador de carga inicial (Splash Screen)
window.addEventListener('load', () => {
    setTimeout(() => {
        document.getElementById('splash').classList.remove('step-flex'); // AQUÍ ESTÁ EL FIX
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

        const restoreButton = () => { btn.innerHTML = originalText; btn.disabled = false; };
        try {
            // La URL de la hoja la gestiona el servidor (entorno o la última guardada).
            const result = await syncConfig();
            if (!result.ok) {
                // Si ya hay una configuración válida vigente se puede continuar con ella;
                // si no, se abre la pantalla de configuración con el motivo.
                const current = await getConfig().catch(() => null);
                const puedeContinuar = result.status !== 400 && current && current.sincronizada;
                if (!puedeContinuar) {
                    restoreButton();
                    openConfigPanel(result);
                    return;
                }
                const sigue = confirm(
                    (result.data.error || "No se pudo actualizar la configuración.") +
                    `\n\nSe usará la última configuración válida (versión ${current.version}, ` +
                    `${new Date(current.sincronizada_en).toLocaleString('es-CO')}).\n¿Continuar?`);
                if (!sigue) {
                    restoreButton();
                    openConfigPanel(result);
                    return;
                }
            }
            console.log("Configuración maestra lista.");
        } catch (error) {
            alert("Error al cargar configuración: " + error.message);
            restoreButton();
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

// Escuchar cuando el usuario arrastra/selecciona el Excel
document.getElementById('excelUpload').addEventListener('change', async (e) => {
    const file = e.target.files[0];
    if (!file) return;

    try {
        console.log("Subiendo archivo...");
        // Llamamos a la función de api.js
        const data = await uploadPlayersExcel(file);
        
        // Guardamos los jugadores procesados
        window.currentPlayers = data.data;
        
        renderValidationTable(window.currentPlayers);
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

// En js/app.js
async function startMassiveProcess() {
    if (!window.currentPlayers || window.currentPlayers.length === 0) {
        alert("No hay jugadores válidos para procesar.");
        return;
    }

    const totalPlayers = window.currentPlayers.length;
    let successCount = 0;

    // Referencias a elementos visuales del nuevo dashboard
    const consoleDiv = document.getElementById('logConsole');
    const btn = document.getElementById('btnStartProcess');
    const statTotal = document.getElementById('statTotal');
    const statSuccess = document.getElementById('statSuccess');
    const statStatus = document.getElementById('statStatus');
    const progressBar = document.getElementById('progressBar');
    const progressPercentage = document.getElementById('progressPercentage');
    const progressLabel = document.getElementById('progressLabel');

    // Inicializar estadísticas visuales
    statTotal.innerText = totalPlayers;
    statSuccess.innerText = "0";
    statStatus.innerText = "Procesando...";
    consoleDiv.innerHTML = ""; // Limpiar consola

    btn.disabled = true;
    btn.innerText = "Procesando Campaña...";
    btn.classList.add('opacity-50', 'cursor-not-allowed');

    const updateProgress = (current, total) => {
        const percent = Math.round((current / total) * 100);
        progressBar.style.width = `${percent}%`;
        progressPercentage.innerText = `${percent}%`;
        progressLabel.innerText = `Procesando ${current} de ${total} registros`;
    };

    const logItem = (message, type = 'info') => {
        let bgClass = "bg-gray-50 text-gray-700 border-gray-100";
        let dotColor = "bg-blue-500";
        let icon = "ℹ️";

        if (type === 'success') {
            bgClass = "bg-green-50 text-green-800 border-green-100";
            dotColor = "bg-green-500";
            icon = "✅";
        } else if (type === 'error') {
            bgClass = "bg-red-50 text-red-800 border-red-100";
            dotColor = "bg-red-500";
            icon = "❌";
        } else if (type === 'warning') {
            bgClass = "bg-amber-50 text-amber-800 border-amber-100";
            dotColor = "bg-amber-500";
            icon = "⚠️";
        }

        const itemHTML = `
            <div class="flex items-center justify-between p-2.5 rounded-lg border ${bgClass} transition-all duration-300">
                <div class="flex items-center space-x-2 truncate">
                    <span class="w-2 h-2 ${dotColor} rounded-full flex-shrink-0"></span>
                    <span class="font-medium truncate">${icon} ${message}</span>
                </div>
            </div>
        `;
        consoleDiv.innerHTML += itemHTML;
        consoleDiv.scrollTop = consoleDiv.scrollHeight;
    };

    try {
        const lugarVisoria = document.getElementById('visoria').value || "Sede Majestic Intercambio";
        
        const formatPdfDate = (dateStr) => {
            if (!dateStr) return "";
            const [year, month, day] = dateStr.split('-');
            return `${day}/${month}/${year}`;
        };

        const d1 = formatPdfDate(document.getElementById('date1').value) || "30/09/2026";
        const d2 = formatPdfDate(document.getElementById('date2').value) || "30/10/2026";
        const d3 = formatPdfDate(document.getElementById('date3').value) || "15/12/2026";
        
        const payloadPlayers = window.currentPlayers.map(p => ({
            ...p,
            VisoriaLocation: lugarVisoria,
            PaymentDate1: d1,
            PaymentDate2: d2,
            PaymentDate3: d3
        }));

        logItem("Iniciando generación masiva de PDFs en el servidor...", "info");
        statStatus.innerText = "Generando PDFs...";
        
        const pdfRes = await generatePDFs(payloadPlayers);
        logItem(`Se generaron ${pdfRes.generated_count} PDFs correctamente en el servidor.`, "success");

        logItem("Conectando con Meta API y Chatwoot para envío masivo...", "info");
        statStatus.innerText = "Enviando por WhatsApp...";

        // Un incremento por destinatario: solo cuentan los mensajes de resultado
        // (✅ enviado, ❌ error, ⚠️ omitido), no los "Enviando...".
        let processedCount = 0;
        let failedCount = 0;
        successCount = 0;
        await sendWhatsAppMessages(payloadPlayers, (msg) => {
            if (msg.startsWith("✅")) {
                successCount++;
                statSuccess.innerText = successCount;
                logItem(msg, "success");
            } else if (msg.startsWith("❌") || msg.startsWith("⚠️")) {
                failedCount++;
                logItem(msg, msg.startsWith("⚠️") ? "warning" : "error");
            } else {
                logItem(msg, "info");
                return;
            }

            processedCount++;
            updateProgress(processedCount, totalPlayers);
        });

        // "Aceptado por Meta" no es "entregado": la confirmación real llega por webhook (Fase 5).
        if (failedCount === 0 && successCount === totalPlayers) {
            statStatus.innerText = "Finalizado: todos aceptados por Meta";
            logItem(`Campaña finalizada: ${successCount} mensajes aceptados por Meta (entrega aún sin confirmar).`, "info");
            alert(`${successCount} mensajes fueron aceptados por Meta. La entrega aún no se puede confirmar desde este panel.`);
        } else {
            statStatus.innerText = `Finalizado con ${failedCount} fallidos u omitidos`;
            logItem(`Campaña finalizada: ${successCount} aceptados por Meta, ${failedCount} fallidos u omitidos de ${totalPlayers}.`, "warning");
            alert(`Campaña finalizada con novedades:\n${successCount} aceptados por Meta\n${failedCount} fallidos u omitidos\nRevisa el panel.`);
        }

    } catch (error) {
        statStatus.innerText = "Error crítico";
        logItem(`Error Crítico: ${error.message}`, "error");
        alert("Ocurrió un error en el proceso. Revisa el panel.");
    } finally {
        btn.innerText = "PROCESO FINALIZADO";
        btn.classList.remove('bg-green-500', 'hover:bg-green-600');
        btn.classList.add('bg-blue-600', 'hover:bg-blue-700');
        btn.disabled = false;
        btn.classList.remove('opacity-50', 'cursor-not-allowed');
    }
}