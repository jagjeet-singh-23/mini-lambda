FROM node:20-slim

WORKDIR /app

# Create a lightweight HTTP server to execute code sent via POST
RUN cat <<'EOF' > server.js
const http = require('http');

const server = http.createServer((req, res) => {
    if (req.method === 'POST') {
        let body = '';
        req.on('data', chunk => {
            body += chunk.toString();
        });
        req.on('end', async () => {
            try {
                const payload = JSON.parse(body);
                const codeB64 = payload.code || '';
                const inputB64 = payload.input || '';
                
                const code = Buffer.from(codeB64, 'base64').toString('utf-8');
                let event = {};
                if (inputB64) {
                    try {
                        const inputStr = Buffer.from(inputB64, 'base64').toString('utf-8');
                        if (inputStr) {
                            event = JSON.parse(inputStr);
                        }
                    } catch (e) {}
                }

                // Redirect console.log to capture output
                let output = '';
                const originalLog = console.log;
                console.log = (...args) => {
                    output += args.map(a => typeof a === 'object' ? JSON.stringify(a) : a).join(' ') + '\n';
                };

                let result = null;
                try {
                    // Evaluate code
                    const script = `
                        ${code}
                        if (typeof handler === 'function') {
                            return handler;
                        }
                        return null;
                    `;
                    const handlerFunc = eval(`(() => { ${script} })()`);
                    
                    if (handlerFunc) {
                        result = await Promise.resolve(handlerFunc(event, {}));
                    }
                } finally {
                    // Restore console.log
                    console.log = originalLog;
                }

                if (result !== null && result !== undefined) {
                    output += JSON.stringify(result);
                }

                res.writeHead(200, { 'Content-Type': 'application/json' });
                res.end(JSON.stringify({ output: output.trim() }));
            } catch (error) {
                res.writeHead(500, { 'Content-Type': 'application/json' });
                res.end(JSON.stringify({ error: error.toString() }));
            }
        });
    } else {
        res.writeHead(404);
        res.end();
    }
});

const PORT = 8080;
server.listen(PORT, () => {
    console.log(`Server listening on port ${PORT}`);
});
EOF

EXPOSE 8080

CMD ["node", "server.js"]
