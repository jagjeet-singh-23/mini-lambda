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
  count                  = 5
  ami                    = data.aws_ami.amazon_linux_2023.id
  instance_type          = "c6a.2xlarge"
  vpc_security_group_ids = [aws_security_group.jmeter_sg.id]

  user_data = <<-EOF
              #!/bin/bash
              dnf update -y
              dnf install -y java-17-amazon-corretto wget tmux
              wget https://dlcdn.apache.org//jmeter/binaries/apache-jmeter-5.6.3.tgz
              tar -xzf apache-jmeter-5.6.3.tgz
              EOF

  tags = {
    Name = "JMeter-Worker-${count.index + 1}"
    Role = "LoadGenerator"
  }
}

output "jmeter_worker_ips" {
  value = aws_instance.jmeter_worker[*].public_ip
}
