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
        // El torneo de origen es obligatorio (va en el encabezado de cada propuesta).
        if (!document.getElementById('torneo').value.trim()) {
            alert("Escribe el nombre del torneo donde se becó a los jugadores.");
            document.getElementById('torneo').focus();
            return;
        }
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

    if (step === 3 && !(await prepareStep3())) return;

    document.getElementById('step1').classList.replace('step-active', 'step-hidden');
    document.getElementById('step2').classList.replace('step-active', 'step-hidden');
    document.getElementById('step3').classList.replace('step-active', 'step-hidden');
    
    document.getElementById('step' + step).classList.replace('step-hidden', 'step-active');
}

// Variable global para guardar los niños mientras avanzamos por los pasos
window.currentPlayers = [];

// Fechas de pago de las casillas del paso 1 (el servidor valida orden y huecos).
function paymentDateFields() {
    return {
        fecha_pago_1: document.getElementById('date1').value,
        fecha_pago_2: document.getElementById('date2').value,
        fecha_pago_3: document.getElementById('date3').value,
    };
}

// Escuchar cuando el usuario arrastra/selecciona el Excel: se crea una campaña (borrador validado).
document.getElementById('excelUpload').addEventListener('change', async (e) => {
    const file = e.target.files[0];
    if (!file) return;

    try {
        const result = await createCampaign(file, {
            torneo: document.getElementById('torneo').value,
            requerir_ambos: document.getElementById('requireBoth').checked ? 'true' : 'false',
            ...paymentDateFields(),
        });
        if (!result.ok) throw new Error(result.data.error || 'No se pudo procesar el archivo.');

        window.currentCampaign = result.data.campaign;
        window.currentPlayers = [];
        campaignView.show(result.data);
    } catch (error) {
        alert("Error: " + error.message);
    } finally {
        e.target.value = ''; // permite volver a subir el mismo archivo ya corregido
    }
});

// Antes del paso 3: confirma lo que no se enviará y trae los destinatarios VIGENTES del servidor
// (con las correcciones hechas en la tabla) en el formato del flujo de envío actual.
async function prepareStep3() {
    const campaign = window.currentCampaign;
    if (!campaign) {
        alert("Primero sube el Excel de jugadores.");
        return false;
    }
    const r = campaignView.current().resumen;
    if (r.con_errores > 0) {
        const sigue = confirm(`${r.con_errores} fila(s) tienen errores y NO se enviarán hasta corregirlas.\n` +
            `Se procesarán ${r.validas} fila(s) válidas.\n\n¿Continuar de todas formas?`);
        if (!sigue) return false;
    }
    if (r.validas === 0) {
        alert("No hay filas válidas para enviar.");
        return false;
    }
    const players = await getCampaignPlayers(campaign.id);
    if (!players.ok) {
        alert("No se pudo preparar el envío: " + (players.data.error || "error del servidor"));
        return false;
    }
    window.currentPlayers = players.data.data;
    return true;
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
        // Torneo y fechas de pago: son los de la campaña (validados por el servidor), no los de la casilla.
        const campaign = window.currentCampaign;
        const formatPdfDate = (iso) => {
            if (!iso) return "";
            const [year, month, day] = iso.split('-');
            return `${day}/${month}/${year}`;
        };
        const [d1, d2, d3] = [0, 1, 2].map(i => formatPdfDate((campaign.fechas_pago || [])[i]));

        const payloadPlayers = window.currentPlayers.map(p => ({
            ...p,
            VisoriaLocation: campaign.torneo,
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