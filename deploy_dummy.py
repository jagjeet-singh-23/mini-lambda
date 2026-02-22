import base64
import io
import json
import sys
import urllib.request
import zipfile


def create_dummy_function():
    # Example pure Python function code
    code = """
def handler(event, context):
    name = event.get('name', 'World')
    return {"message": f"Hello, {name}! This is running on mini-lambda."}
"""

    # Create an in-memory zip file
    zip_buffer = io.BytesIO()
    with zipfile.ZipFile(zip_buffer, "a", zipfile.ZIP_DEFLATED, False) as zip_file:
        zip_file.writestr("main.py", code)

    zip_bytes = zip_buffer.getvalue()
    base64_zip = base64.b64encode(zip_bytes).decode("utf-8")

    payload = {
        "name": "helloworld",
        "runtime": "python3.11",
        "handler": "main.handler",
        "package_data": base64_zip,
        "webhook_url": "",  # No webhook needed for this test
        "timeout": 30,
        "memory": 128,
    }
    # Explicitly using the IPv4 address to avoid Python's IPv6 resolution hang
    gateway_url = "http://192.168.1.111:8080"
    url = f"{gateway_url}/functions"

    data = json.dumps(payload).encode("utf-8")

    req = urllib.request.Request(url, data=data, method="POST")
    req.add_header("Content-Type", "application/json")

    print(f"Deploying function to {url}...")
    try:
        response = urllib.request.urlopen(req)
        response_data = json.loads(response.read().decode("utf-8"))
        print("\n✅ Deployment Request Accepted!")
        print(f"Function ID: {response_data.get('function_id')}")
        print(f"Job ID:      {response_data.get('job_id')}")
        print(
            "\nThe worker should be building it now. You can use this Function ID in JMeter!"
        )
    except urllib.error.URLError as e:
        print(f"\n❌ Error: {e}")
        if hasattr(e, "read"):
            print(e.read().decode())


if __name__ == "__main__":
    create_dummy_function()
