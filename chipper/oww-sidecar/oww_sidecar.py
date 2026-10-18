"""openWakeWord sidecar for wire-pod's oww wake word detector (pkg/oww).

Reads raw mono s16le mic samples from stdin, resamples them from --in-rate to 16 kHz with soxr (Vector's mic
really runs at 15625 Hz although MicTap labels it 16000), scores every 1280-sample frame (80 ms at 16 kHz) with
an openWakeWord model, and writes one JSON line per frame whose score reaches --report:

    {"score": 0.93, "sample": 123456}

"sample" is the input position (in --in-rate samples since the process started) at the end of that frame.
wire-pod applies the threshold and the refractory period. Status goes to stderr. Exits at the end of stdin.
"""

import argparse
import json
import os
import sys

import numpy as np

RATE = 16000
FRAME = 1280


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", required=True, help="openWakeWord .onnx model")
    ap.add_argument("--in-rate", type=int, default=15625, help="true rate of the samples on stdin")
    ap.add_argument("--report", type=float, default=0.1, help="lowest score written to stdout")
    args = ap.parse_args()

    import soxr
    from openwakeword.model import Model

    model = Model(wakeword_models=[args.model], inference_framework="onnx")
    key = next(iter(model.models))
    stream = soxr.ResampleStream(args.in_rate, RATE, 1, dtype="int16") if args.in_rate != RATE else None
    print(f"oww-sidecar: ready, model {key}, input {args.in_rate} Hz, report >= {args.report}",
          file=sys.stderr, flush=True)

    pending = np.zeros(0, np.int16)
    odd = b""
    frames = 0  # 16 kHz frames scored so far
    out = sys.stdout
    fd = sys.stdin.fileno()
    while True:
        chunk = os.read(fd, 65536)
        last = not chunk
        data, odd = odd + chunk, b""
        if len(data) % 2:
            data, odd = data[:-1], data[-1:]
        pcm = np.frombuffer(data, np.int16)
        if stream is not None:
            pcm = stream.resample_chunk(pcm, last=last)
        pending = np.concatenate([pending, pcm])
        n = len(pending) // FRAME
        for i in range(n):
            score = float(model.predict(pending[i * FRAME:(i + 1) * FRAME])[key])
            frames += 1
            if score >= args.report:
                sample = round(frames * FRAME * args.in_rate / RATE)
                out.write(json.dumps({"score": round(score, 4), "sample": sample}) + "\n")
        if n:
            out.flush()
            pending = pending[n * FRAME:]
        if last:
            return


if __name__ == "__main__":
    try:
        main()
    except (BrokenPipeError, KeyboardInterrupt):
        sys.exit(0)
