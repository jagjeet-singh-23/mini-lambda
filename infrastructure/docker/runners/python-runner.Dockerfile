FROM python:3.11-slim

WORKDIR /app

# Create a lightweight HTTP server to execute code sent via POST
RUN cat <<'EOF' > server.py
import http.server
import socketserver
import json
import base64
import sys
import io
import contextlib

class ExecutionHandler(http.server.SimpleHTTPRequestHandler):
    def do_POST(self):
        content_length = int(self.headers['Content-Length'])
        post_data = self.rfile.read(content_length)
        
        try:
            payload = json.loads(post_data.decode('utf-8'))
            code_b64 = payload.get('code', '')
            input_b64 = payload.get('input', '')
            
            code = base64.b64decode(code_b64).decode('utf-8')
            event = {}
            if input_b64:
                try:
                    event_str = base64.b64decode(input_b64).decode('utf-8')
                    if event_str:
                        event = json.loads(event_str)
                except Exception:
                    pass
            
            # Capture output
            f = io.StringIO()
            with contextlib.redirect_stdout(f):
                # Execute the code in the global namespace
                # It is expected to define a 'handler(event, context)' function
                exec_globals = {}
                exec(code, exec_globals)
                
                result = None
                if 'handler' in exec_globals:
                    result = exec_globals['handler'](event, {})
            
            output = f.getvalue()
            if result is not None:
                output += json.dumps(result)
                
            self.send_response(200)
            self.send_header('Content-type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps({'output': output}).encode('utf-8'))
            
        except Exception as e:
            self.send_response(500)
            self.send_header('Content-type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps({'error': str(e)}).encode('utf-8'))

    def log_message(self, format, *args):
        pass

PORT = 8080
with socketserver.TCPServer(("", PORT), ExecutionHandler) as httpd:
    print(f"Serving at port {PORT}")
    httpd.serve_forever()
EOF

EXPOSE 8080

CMD ["python", "server.py"]
