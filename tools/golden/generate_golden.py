#!/usr/bin/env python3
"""Genera los GOLDEN FILES auténticos de los documentos, con el código REAL del cliente.

Ejecuta `crear_docx` de MajesticBecas (python-docx) sobre sus plantillas con jugadores de prueba y guarda,
por cada combinación plantilla × beca, SOLO lo que cambió respecto a la plantilla (párrafos y celdas).
Así los archivos no contienen el texto fijo de las plantillas (trae la cédula y las cuentas de la
coordinadora) y pueden versionarse.

El test de Go (internal/adapters/docx, TestGoldenDelCliente) compara contra ellos el .docx que genera Go.

Uso (en el PC del cliente, con su entorno de MajesticBecas activado: necesita python-docx):

    python tools/golden/generate_golden.py "C:\\ruta\\MajesticBecas\\majestic_becas" internal/adapters/docx/testdata/golden

NOTA: este script NO se pudo ejecutar al escribirlo (el entorno de desarrollo no tenía python-docx);
si falla, el error estará en este script y no en la comparación.
"""
import json
import sys
import tempfile
from pathlib import Path

base = Path(sys.argv[1]).resolve()
salida = Path(sys.argv[2])
sys.path.insert(0, str(base))

from docx import Document  # noqa: E402
from becas import config as cfgmod  # noqa: E402
from becas.excel_reader import Fila  # noqa: E402
from becas.generador import crear_docx  # noqa: E402

TORNEO, EMISION = "Copa Prueba", "05/11/2026"
FECHAS = ["01/12/2026", "01/01/2027", "01/02/2027"]


def foto(ruta):
    """Texto de los párrafos del cuerpo y de las celdas de las tablas (como los ve python-docx)."""
    d = Document(str(ruta))
    return {
        "parrafos": [p.text for p in d.paragraphs],
        "tablas": [[[c.text for c in fila.cells] for fila in t.rows] for t in d.tables],
    }


def diferencias(antes, despues):
    cambios = {"parrafos": {}, "celdas": {}}
    for i, (a, b) in enumerate(zip(antes["parrafos"], despues["parrafos"])):
        if a != b:
            cambios["parrafos"][str(i)] = b
    for t, (ta, tb) in enumerate(zip(antes["tablas"], despues["tablas"])):
        for r, (fa, fb) in enumerate(zip(ta, tb)):
            for c, (ca, cb) in enumerate(zip(fa, fb)):
                if ca != cb:
                    cambios["celdas"][f"{t},{r},{c}"] = cb
    return cambios


cfg = cfgmod.cargar()
salida.mkdir(parents=True, exist_ok=True)
for plantilla in cfgmod.PLANTILLAS:
    original = foto(cfgmod.ruta_plantilla(plantilla))
    for beca in cfgmod.BECAS_CON_PROPUESTA:
        plan = cfg["planes_pago"][beca]
        fila = Fila(fila=2, jugador="PRUEBA MAJESTIC", club="Club de Prueba", beca_raw=f"{beca}%", beca=beca, anio=2012,
                    acudiente="Acudiente de Prueba", correo="acudiente@example.com", movil="3209323449", plantilla=plantilla)
        with tempfile.TemporaryDirectory() as tmp:
            ruta = Path(tmp) / "x.docx"
            crear_docx(fila, cfg, TORNEO, EMISION, FECHAS[:len(plan)], ruta)
            generado = foto(ruta)
        golden = {
            "generador": "majestic-python",
            "plantilla": plantilla, "beca": beca, "torneo": TORNEO, "emision": EMISION, "fechas": FECHAS[:len(plan)],
            "cambios": diferencias(original, generado),
            "columnas_tabla_pagos": [len(f) for f in generado["tablas"][1]],
            "num_parrafos": len(generado["parrafos"]),
        }
        (salida / f"{plantilla}_{beca}.json").write_text(json.dumps(golden, ensure_ascii=False, indent=1), encoding="utf-8")
        print("ok", plantilla, beca)
