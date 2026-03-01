provider "aws" {
  region = "ap-south-1"
}

data "aws_ami" "amazon_linux_2023" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-2023*-x86_64"]
  }
}

resource "aws_security_group" "jmeter_sg" {
  name        = "jmeter-load-generator-sg"
  description = "Allow outbound traffic for JMeter"

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "jmeter_worker" {
  count                  = 4
  ami                    = data.aws_ami.amazon_linux_2023.id
  instance_type          = "c6a.xlarge"
  vpc_security_group_ids = [aws_security_group.jmeter_sg.id]

  user_data = <<-EOF
              #!/bin/bash
              dnf update -y
              dnf install -y wget tmux jq
              wget https://github.com/grafana/k6/releases/download/v0.49.0/k6-v0.49.0-linux-amd64.tar.gz
              tar -xzf k6-v0.49.0-linux-amd64.tar.gz
              mv k6-v0.49.0-linux-amd64/k6 /usr/bin/k6
              EOF

  tags = {
    Name = "K6-Worker-${count.index + 1}"
    Role = "LoadGenerator"
  }
}

output "k6_worker_ips" {
  value = aws_instance.jmeter_worker[*].public_ip
}
